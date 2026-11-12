package toolkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	astisub "github.com/asticode/go-astisub"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FFmpeg probes bytes, not names or MIME. Give it one seekable descriptor,
// without file/network protocols: playlists cannot read other local files or
// fetch URLs. This also supports MP4 with its index at the end of the file.
func runMediaCommand(ctx context.Context, input, program string, before, after []string, output io.Writer) error {
	return runWithDiskSpace(ctx, filepath.Dir(input), func(ctx context.Context) error {
		file, err := os.Open(input)
		if err != nil {
			return err
		}
		defer file.Close()
		args := append(append([]string{}, before...), "-protocol_whitelist", "fd", "-fd", "3", "-i", "fd:")
		cmd := exec.CommandContext(ctx, program, append(args, after...)...)
		cmd.ExtraFiles = []*os.File{file}
		cmd.Stdout = output
		return runBounded(cmd)
	})
}

func transcribeMedia(ctx context.Context, config MediaConfig, input string, job Job) (string, error) {
	if config.WhisperURL == "" {
		return "", errors.New("语音转写服务尚未配置。")
	}
	if err := checkMedia(ctx, input); err != nil {
		return "", err
	}
	segments, err := os.MkdirTemp(job.Dir, ".speech-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(segments)
	if err := job.Report("自动提取音轨并分段（FFmpeg）"); err != nil {
		return "", err
	}
	// Native segment muxer preserves every audio sample and bounds each Whisper
	// request to ten minutes of mono 16 kHz PCM, irrespective of source size.
	err = runMediaCommand(ctx, input, "ffmpeg", []string{"-nostdin", "-v", "error"}, []string{
		"-map", "0:a:0", "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-threads", "1",
		"-f", "segment", "-segment_time", "600", "-reset_timestamps", "1", filepath.Join(segments, "%09d.wav"),
	}, nil)
	if err != nil {
		if errors.Is(err, errDiskSpace) {
			return "", err
		}
		return "", errors.New("音轨解码失败，文件可能损坏、需要外部资源或格式不受 FFmpeg 支持。")
	}
	files, err := filepath.Glob(filepath.Join(segments, "*.wav"))
	if err != nil || len(files) == 0 {
		return "", errors.New("未提取到音频。")
	}
	full, err := os.OpenFile(filepath.Join(job.Dir, "transcript.txt"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer full.Close()
	vtt, err := os.OpenFile(filepath.Join(job.Dir, "transcript.vtt"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer vtt.Close()
	if _, err = io.WriteString(diskWriter{vtt}, "WEBVTT\n\n"); err != nil {
		return "", err
	}
	offset := time.Duration(0)
	cueCount := 0
	var transcript strings.Builder
	hasText, truncated := false, false
	for i, file := range files {
		if err := job.Report(fmt.Sprintf("GPU 转写：第 %d / %d 段", i+1, len(files))); err != nil {
			return "", err
		}
		duration, err := mediaDuration(ctx, file)
		if err != nil {
			return "", err
		}
		speech, err := transcribeResponse(ctx, config.WhisperURL, file, config.transport, "verbose_json")
		text := speech.Text
		if err != nil {
			return "", fmt.Errorf("第 %d 段转写失败：%w", i+1, err)
		}

		subtitles := astisub.NewSubtitles()
		for _, segment := range speech.Segments {
			if strings.TrimSpace(segment.Text) == "" {
				continue
			}
			if math.IsNaN(segment.Start) || math.IsNaN(segment.End) || segment.Start < 0 || segment.End <= segment.Start || segment.End > duration.Seconds()+1 {
				return "", errors.New("转写返回无效字幕时间。")
			}
			subtitles.Items = append(subtitles.Items, &astisub.Item{StartAt: offset + time.Duration(segment.Start*float64(time.Second)), EndAt: offset + time.Duration(segment.End*float64(time.Second)), Lines: []astisub.Line{{Items: []astisub.LineItem{{Text: strings.TrimSpace(segment.Text)}}}}})
		}
		if len(subtitles.Items) > 0 {
			var buffer bytes.Buffer
			if err := subtitles.WriteToWebVTT(&buffer); err != nil {
				return "", err
			}
			body := strings.TrimPrefix(buffer.String(), "WEBVTT\n\n")
			if _, err := io.WriteString(diskWriter{vtt}, body); err != nil {
				return "", err
			}
			cueCount += len(subtitles.Items)
		} else if text != "" {
			return "", errors.New("转写服务未返回字幕时间，请检查 verbose_json 支持。")
		}
		offset += duration
		os.Remove(file)
		if text != "" {
			if hasText {
				text = "\n" + text
			}
			hasText = true
			if _, err := io.WriteString(diskWriter{full}, text); err != nil {
				return "", err
			}
			// Keep task JSON/browser memory bounded while the downloadable text
			// retains the complete transcript, with no aggregate text rejection.
			room := 600000 - transcript.Len()
			if len(text) > room {
				text = text[:room]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
				truncated = true
			}
			transcript.WriteString(text)
		}
	}
	if !hasText {
		return "", errors.New("未识别到有效语音，未生成转写结果。")
	}
	if err := full.Close(); err != nil {
		return "", err
	}

	if err := vtt.Close(); err != nil {
		return "", err
	}
	if cueCount > 0 {
		if err := runMediaCommand(ctx, filepath.Join(job.Dir, "transcript.vtt"), "ffmpeg", []string{"-nostdin", "-v", "error"}, []string{"-f", "srt", "-y", filepath.Join(job.Dir, "transcript.srt")}, nil); err != nil {
			return "", errors.New("字幕导出失败。")
		}
	}
	if truncated {
		transcript.WriteString("\n\n文字较长，此处显示部分内容；完整文字请下载保存（临时文件保留 24 小时）。")
	}
	return transcript.String(), nil
}

// Avoid holding a download slot forever if the peer stops sending bytes.
type idleConnection struct{ net.Conn }

func (c idleConnection) Read(p []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	return c.Conn.Read(p)
}

func mediaDuration(ctx context.Context, path string) (time.Duration, error) {
	out := &limitedBuffer{limit: 4096}
	if err := runMediaCommand(ctx, path, "ffprobe", []string{"-v", "error"}, []string{"-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1"}, out); err != nil {
		return 0, errors.New("无法读取分段时长。")
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 31536000 {
		return 0, errors.New("无效媒体时长。")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
