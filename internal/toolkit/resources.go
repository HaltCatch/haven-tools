package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html/charset"
)

var imageTools = []string{"image-compress", "image-resize", "image-png", "image-jpeg", "image-webp"}
var resourceNames = append([]string{"file-download", "article-extract", "document-text", "media-transcribe", "media-audio", "media-clip", "qr-url"}, imageTools...)

func resourceTasks(config MediaConfig) map[string]AsyncTool {
	out := map[string]AsyncTool{}
	for _, name := range resourceNames {
		name := name
		t, _ := findTool(name)
		uploads := name == "document-text" || strings.HasPrefix(name, "image-") || strings.HasPrefix(name, "media-")
		out[name] = AsyncTool{Title: t.Title, Timeout: -1, Artifacts: true, Temporary: true, Uploads: uploads, Notify: name == "media-transcribe" || name == "document-text",
			Validate: func(a Arguments) error {
				if name == "media-clip" {
					if err := validateClip(a.Clip); err != nil {
						return err
					}
				} else if a.Clip != nil {
					return errors.New("此工具不接受截取参数。")
				}
				if uploads && strings.HasPrefix(a.Text, "upload:") && len(a.Text) == 71 {
					return nil
				}
				_, e := resourceURL(a.Text)
				return e
			},
			Run: func(ctx context.Context, a Arguments, j Job) (Result, error) {
				return runResource(ctx, name, a, j, config)
			},
		}
	}
	return out
}
func publish(j Job, name, label, download, ct string) error {
	if j.Publish == nil {
		return nil
	}
	return j.Publish(Artifact{Name: name, Label: label, DownloadName: download, ContentType: ct})
}
func runResource(ctx context.Context, name string, a Arguments, j Job, config MediaConfig) (Result, error) {
	input := filepath.Join(j.Dir, "input")
	meta := resource{Filename: "上传文件"}
	if !strings.HasPrefix(a.Text, "upload:") {
		if e := j.Report("下载资源到临时目录"); e != nil {
			return Result{}, e
		}
		var limit int64 // No per-file byte ceiling; stream to available disk.
		if name == "article-extract" {
			limit = 4 << 20
		}
		var e error
		meta, e = fetchResource(ctx, a.Text, input, limit)
		if e != nil {
			return Result{}, e
		}
	}
	result := resultFor(name, "")
	result.Fields = []Field{{"来源", meta.Filename}}
	switch name {
	case "file-download":
		if strings.Contains(meta.ContentType, "html") {
			return Result{}, errors.New("链接返回网页，不能作为文件下载；请使用网页正文提取。")
		}
		if e := os.Rename(input, filepath.Join(j.Dir, "download.bin")); e != nil {
			return Result{}, e
		}
		if e := publish(j, "download.bin", "下载文件", meta.Filename, meta.ContentType); e != nil {
			return Result{}, e
		}
		result.Text = "文件已下载到临时目录，请点击下载文件。"
		result.Fields = append(result.Fields, Field{"文件大小", fmt.Sprintf("%.2f MiB", float64(meta.Bytes)/(1<<20))}, Field{"类型", meta.ContentType})
		result.Links = []Link{{"下载文件", j.URL + "/files/download.bin"}}
	case "article-extract":
		if !strings.Contains(meta.ContentType, "html") {
			return Result{}, errors.New("该链接没有返回 HTML 网页。")
		}
		raw, e := os.ReadFile(input)
		if e != nil {
			return Result{}, e
		}
		reader, e := charset.NewReader(bytes.NewReader(raw), meta.ContentType)
		if e != nil {
			return Result{}, e
		}
		u, _ := url.Parse(meta.URL)
		article, e := readability.FromReader(reader, u)
		var plain, html bytes.Buffer
		if e == nil {
			e = article.RenderText(&plain)
			if e == nil {
				e = article.RenderHTML(&html)
			}
		}
		if e != nil || len(strings.TrimSpace(plain.String())) < 80 {
			return Result{}, errors.New("未提取到有效正文；需要登录或 JavaScript 渲染的页面暂不支持。")
		}
		markdown, e := htmltomarkdown.ConvertString(html.String())
		if e != nil {
			return Result{}, errors.New("正文转换失败。")
		}
		text := "# " + article.Title() + "\n\n" + article.Byline() + "\n\n来源：" + meta.URL + "\n\n" + markdown
		if len(text) > 700000 {
			return Result{}, errors.New("正文过大，无法保存任务结果。")
		}
		result.Title = article.Title()
		result.Text = text
		if e := os.WriteFile(filepath.Join(j.Dir, "article.md"), []byte(text), 0600); e != nil {
			return Result{}, e
		}
		if e := publish(j, "article.md", "下载 Markdown", "article.md", "text/markdown; charset=utf-8"); e != nil {
			return Result{}, e
		}
		result.Links = []Link{{"下载 Markdown", j.URL + "/files/article.md"}, {"打开原文", meta.URL}}
	case "media-transcribe":
		if e := j.Report("GPU 语音转写（现有 Whisper / Vulkan）"); e != nil {
			return Result{}, e
		}
		text, e := transcribeMedia(ctx, config, input, j)
		if e != nil {
			return Result{}, e
		}
		result.Text = text
		if e := publish(j, "transcript.txt", "下载文字", "transcript.txt", "text/plain; charset=utf-8"); e != nil {
			return Result{}, e
		}
		result.Links = []Link{{"下载文字", j.URL + "/files/transcript.txt"}}
		for _, format := range []string{"srt", "vtt"} {
			filename := "transcript." + format
			ct := "text/plain; charset=utf-8"
			if format == "vtt" {
				ct = "text/vtt; charset=utf-8"
			}
			if err := publish(j, filename, "下载 "+strings.ToUpper(format)+" 字幕", filename, ct); err != nil {
				return Result{}, err
			}
			result.Links = append(result.Links, Link{"下载 " + strings.ToUpper(format) + " 字幕", j.URL + "/files/" + filename})
		}
	case "document-text":
		if e := j.Report("提取文字（通用 OCR 服务 / PDF 文字层优先）"); e != nil {
			return Result{}, e
		}
		text, fields, e := extractDocument(ctx, config, input)
		if e != nil {
			return Result{}, e
		}
		if e := os.WriteFile(filepath.Join(j.Dir, "text.txt"), []byte(text+"\n"), 0600); e != nil {
			return Result{}, e
		}
		if e := publish(j, "text.txt", "下载文字", "text.txt", "text/plain; charset=utf-8"); e != nil {
			return Result{}, e
		}
		result.Text = text
		result.Fields = append(result.Fields, fields...)
		result.Links = []Link{{"下载文字", j.URL + "/files/text.txt"}}
	case "media-clip":
		return clipMedia(ctx, a, j, input)
	case "media-audio":
		if e := checkMedia(ctx, input); e != nil {
			return Result{}, e
		}
		if e := j.Report("提取音频（FFmpeg，仅处理音轨）"); e != nil {
			return Result{}, e
		}
		if e := runMediaCommand(ctx, input, "ffmpeg", []string{"-nostdin", "-v", "error"}, []string{"-map", "0:a:0", "-vn", "-c:a", "aac", "-b:a", "128k", "-threads", "1", "-y", filepath.Join(j.Dir, "audio.m4a")}, nil); e != nil {
			if errors.Is(e, errDiskSpace) {
				return Result{}, e
			}
			return Result{}, errors.New("音轨提取失败，文件可能无音频或格式不支持。")
		}
		if e := publish(j, "audio.m4a", "下载音频", "audio.m4a", "audio/mp4"); e != nil {
			return Result{}, e
		}
		result.Text = "已提取音轨为 M4A（AAC 128 kbps）。"
		result.Links = []Link{{"下载音频", j.URL + "/files/audio.m4a"}}
	case "qr-url":
		file, e := os.Open(input)
		if e != nil {
			return Result{}, e
		}
		result, e := decodeQR(ctx, file)
		file.Close()
		os.Remove(input)
		return result, e
	default:
		if config.Processor == "" {
			return Result{}, errors.New("文档／图片处理组件尚未配置。")
		}
		if e := j.Report("处理图片（格式／尺寸与元数据清理）"); e != nil {
			return Result{}, e
		}
		python := config.Python
		if python == "" {
			python = "python3"
		}
		output := &limitedBuffer{limit: 800001}
		if e := runWithDiskSpace(ctx, j.Dir, func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, python, config.Processor, name, input, j.Dir)
			cmd.Stdout = output
			return runBounded(cmd)
		}); e != nil {
			if errors.Is(e, errDiskSpace) {
				return Result{}, e
			}
			return Result{}, errors.New("图片处理失败：支持不超过 3200 万像素的图片，损坏或不支持的文件无法处理。")
		}
		var processed struct {
			Text        string  `json:"text"`
			Fields      []Field `json:"fields"`
			File        string  `json:"file"`
			ContentType string  `json:"contentType"`
		}
		if output.Len() > 800000 || json.Unmarshal(output.Bytes(), &processed) != nil || !artifactName.MatchString(processed.File) {
			return Result{}, errors.New("处理组件返回无效结果。")
		}
		result.Text = processed.Text
		result.Fields = append(result.Fields, processed.Fields...)
		if e := publish(j, processed.File, "下载结果", processed.File, processed.ContentType); e != nil {
			return Result{}, e
		}
		result.Links = []Link{{"下载结果", j.URL + "/files/" + processed.File}}
	}
	os.Remove(input)
	result.Description = "临时文件保留 24 小时，到期自动清理；服务重建也可能提前清理。任务和文字结果继续保留。"
	return result, nil
}
func runBounded(cmd *exec.Cmd) error {
	boundProcess(cmd)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1", "OMP_THREAD_LIMIT=1"}
	cmd.Stderr = &limitedBuffer{limit: 8192}
	cmd.WaitDelay = 3 * time.Second
	return cmd.Run()
}
func checkMedia(ctx context.Context, input string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output := &limitedBuffer{limit: 16384}
	if runMediaCommand(ctx, input, "ffprobe", []string{"-v", "error"}, []string{"-show_entries", "stream=codec_type", "-of", "json"}, output) != nil {
		return errors.New("文件不是可识别的音频／视频。")
	}
	var data struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
	}
	if json.Unmarshal(output.Bytes(), &data) != nil {
		return errors.New("无法读取媒体信息。")
	}
	audio := false
	for _, s := range data.Streams {
		if s.Type == "audio" {
			audio = true
		}
	}
	if !audio {
		return errors.New("文件中没有可用音轨。")
	}
	return nil
}
