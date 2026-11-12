package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Media orchestration reuses yt-dlp/FFmpeg and the existing GPU Whisper server.
// No model selection or weights live in this process.
type MediaConfig struct {
	WhisperURL, Python, Downloader, Processor string
	OCRURL, OCRToken                          string
	transport                                 http.RoundTripper
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.limit {
		b.Buffer.Write(p[:min(len(p), b.limit-b.Len())])
	}
	return n, nil
}

func mediaTask(config MediaConfig) AsyncTool {
	tool, _ := findTool("bilibili-transcribe")
	return AsyncTool{Title: tool.Title, Timeout: -1, Artifacts: true, Notify: true, Temporary: true,
		Validate: func(a Arguments) error { _, err := bilibiliID(a.Text); return err },
		Run:      func(ctx context.Context, a Arguments, job Job) (Result, error) { return runMedia(ctx, a, job, config) },
	}
}

func runMedia(ctx context.Context, args Arguments, job Job, config MediaConfig) (Result, error) {
	if config.WhisperURL == "" || config.Downloader == "" {
		return Result{}, errors.New("视频转写服务尚未配置。")
	}
	if err := job.Report("读取视频信息"); err != nil {
		return Result{}, err
	}
	meta, err := ReadBilibili(ctx, args.Text, config.transport)
	if err != nil {
		return Result{}, err
	}
	// Canonical, validated URL only; strip tracking/options before the downloader.
	canonical := meta.Links[0].Href
	original, _ := url.Parse(args.Text)
	if page := original.Query().Get("p"); page != "" {
		// yt-dlp chooses one requested part; never execute a playlist implicitly.
		if len(page) > 4 || strings.Trim(page, "0123456789") != "" || page == "0" {
			return Result{}, errors.New("分 P 编号无效。")
		}
		canonical += "?p=" + page
	}
	if err := job.Report("下载视频（最高 480p，保留原音频）"); err != nil {
		return Result{}, err
	}
	python := config.Python
	if python == "" {
		python = "python3"
	}
	stdout := &limitedBuffer{limit: 16384}
	err = runWithDiskSpace(ctx, job.Dir, func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, python, config.Downloader, canonical, job.Dir)
		boundProcess(cmd)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1"}
		cmd.Stdout, cmd.Stderr = stdout, &limitedBuffer{limit: 8192}
		cmd.WaitDelay = 3 * time.Second
		return cmd.Run()
	})
	if err != nil {
		if errors.Is(err, errDiskSpace) {
			return Result{}, err
		}
		if ctx.Err() != nil {
			return Result{}, errors.New("任务已取消。")
		}
		// Signed CDN URLs in native diagnostics must never reach clients.
		return Result{}, errors.New("B 站下载失败：可能受上游登录、地区、风控或网络限制；也请检查临时存储。")
	}
	var download struct {
		Title    string  `json:"title"`
		Duration float64 `json:"duration"`
		Bytes    int64   `json:"bytes"`
	}
	if json.Unmarshal(stdout.Bytes(), &download) != nil || download.Duration <= 0 || download.Bytes <= 0 {
		return Result{}, errors.New("下载器返回无效结果。")
	}
	video := filepath.Join(job.Dir, "video.mp4")
	info, err := os.Lstat(video)
	if err != nil || !info.Mode().IsRegular() || info.Size() != download.Bytes {
		return Result{}, errors.New("视频文件校验失败。")
	}
	if err := job.Report("GPU 转写（现有 Whisper / Vulkan 服务）"); err != nil {
		return Result{}, err
	}
	transcript, err := transcribeMedia(ctx, config, video, job)
	if err != nil {
		return Result{}, err
	}
	if err := job.Report("保存视频和转写结果"); err != nil {
		return Result{}, err
	}
	for _, f := range []Artifact{{Name: "video.mp4", Label: "下载视频", DownloadName: "video.mp4", ContentType: "video/mp4"}, {Name: "transcript.txt", Label: "下载文字", DownloadName: "transcript.txt", ContentType: "text/plain; charset=utf-8"}} {
		if job.Publish != nil {
			if err := job.Publish(f); err != nil {
				return Result{}, err
			}
		}
	}
	result := resultFor("bilibili-transcribe", transcript)
	result.Title = download.Title
	result.Description = "已下载视频并通过现有 Whisper 服务转写。自动转写可能有误，请对照原视频核实。临时文件保留 24 小时，任务和文字继续保留。"
	result.Fields = []Field{{"时长", fmt.Sprintf("%.1f 秒", download.Duration)}, {"视频大小", fmt.Sprintf("%.1f MiB", float64(download.Bytes)/(1<<20))}, {"转写服务", "Whisper large-v3-turbo · Vulkan / Radeon 780M"}}
	for _, format := range []string{"srt", "vtt"} {
		filename := "transcript." + format
		ct := "text/plain; charset=utf-8"
		if format == "vtt" {
			ct = "text/vtt; charset=utf-8"
		}
		if err := publish(job, filename, "下载 "+strings.ToUpper(format)+" 字幕", filename, ct); err != nil {
			return Result{}, err
		}
	}
	result.Links = []Link{{"下载 SRT 字幕", job.URL + "/files/transcript.srt"}, {"下载 VTT 字幕", job.URL + "/files/transcript.vtt"}, {"下载视频", job.URL + "/files/video.mp4"}, {"下载文字", job.URL + "/files/transcript.txt"}, {"在 B 站打开", canonical}}
	return result, nil
}

type speechSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}
type speechResponse struct {
	Text     string          `json:"text"`
	Segments []speechSegment `json:"segments"`
}

func transcribeFile(ctx context.Context, endpoint, path string, transport http.RoundTripper) (string, error) {
	r, err := transcribeResponse(ctx, endpoint, path, transport, "json")
	if len(r.Text) > 800000 {
		return "", errors.New("转写响应读取失败或过大。")
	}
	return r.Text, err
}

func transcribeResponse(ctx context.Context, endpoint, path string, transport http.RoundTripper, format string) (speechResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return speechResponse{}, err
	}
	defer f.Close()
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		part, e := multipartWriter.CreateFormFile("file", filepath.Base(path))
		if e == nil {
			_, e = io.Copy(part, f)
		}
		for _, field := range [][2]string{{"response_format", format}, {"language", "auto"}, {"temperature", "0"}} {
			if e == nil {
				e = multipartWriter.WriteField(field[0], field[1])
			}
		}
		if e == nil {
			e = multipartWriter.Close()
		}
		writer.CloseWithError(e)
		done <- e
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, reader)
	if err != nil {
		reader.CloseWithError(err)
		<-done
		return speechResponse{}, err
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	client := &http.Client{Transport: transport, Timeout: 50 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	reader.Close()
	<-done
	if err != nil {
		return speechResponse{}, errors.New("GPU 转写服务调用失败或超时。")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return speechResponse{}, fmt.Errorf("GPU 转写服务返回 HTTP %d。", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8000001))
	if err != nil || len(raw) > 8000000 {
		return speechResponse{}, errors.New("转写响应读取失败或过大。")
	}
	var result speechResponse
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope["text"] == nil || json.Unmarshal(raw, &result) != nil {
		return speechResponse{}, errors.New("转写服务返回无效结果。")
	}
	result.Text = strings.TrimSpace(result.Text)
	return result, nil
}
