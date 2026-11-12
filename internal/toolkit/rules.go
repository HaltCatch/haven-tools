package toolkit

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

var base64Command = regexp.MustCompile(`(?is)^(?:base64|b64)(?:[\s：:](.*))?$`)
var urlCommand = regexp.MustCompile(`(?is)^url(?:[\s：:](.*))?$`)
var jsonCommand = regexp.MustCompile(`(?is)^json(?:[\s：:](.*))?$`)
var timestampCommand = regexp.MustCompile(`(?is)^(?:timestamp|时间戳)(?:[\s：:](.*))?$`)
var uuidCommand = regexp.MustCompile(`(?i)^(?:uuid|guid|生成\s*uuid)\s*$`)
var percentEscape = regexp.MustCompile(`(?i)%[\da-f]{2}`)
var timestampNumber = regexp.MustCompile(`^-?\d{10}(?:\d{3})?$`)

func commandPayload(re *regexp.Regexp, text string) (string, bool) {
	match := re.FindStringSubmatch(text)
	if match == nil {
		return "", false
	}
	return match[1], true
}

func Suggest(text, inputKind string) []Action {
	actions := []Action{}
	if inputKind == "image" || inputKind == "file" {
		names := []string{"media-transcribe", "media-audio", "document-text", "media-info", "media-clip", "pdf-workbench"}
		if inputKind == "image" {
			names = append(append([]string{"qr-decode", "document-text"}, imageTools...), "media-transcribe", "media-audio", "images-pdf", "image-metadata")
		}
		for i, name := range names {
			a := action(name, "", 150-i)
			a.InputKind = inputKind
			a.UploadPath = "/v1/files/" + name
			if name == "qr-decode" {
				a.UploadPath = "/v1/image"
			}
			a.Arguments = nil
			actions = append(actions, a)
		}
		return actions
	}
	q := strings.TrimSpace(text)
	if q == "工具" || q == "小工具" || strings.EqualFold(q, "tools") || strings.EqualFold(q, "help") || q == "帮助" {
		seen := map[string]bool{}
		for _, t := range Catalog() {
			group := strings.Split(t.Name, "-")[0]
			if t.Editor {
				group = t.Name
			}
			if seen[group] {
				continue
			}
			seen[group] = true
			a := Action{ID: "directory/" + t.Name, Kind: "fill", Title: t.Category + " · " + strings.Split(t.Title, "（")[0], Detail: t.Description, Icon: t.Icon, Score: 170 - len(actions), Value: t.Example}
			if t.Editor {
				a = action(t.Name, "", 170-len(actions))
			}
			if t.InputKind == "image" {
				a.Kind = "pick-image"
			}
			actions = append(actions, a)
		}
		return actions
	}

	for _, entry := range []struct {
		name  string
		words []string
	}{
		{"pdf-workbench", []string{"pdf", "合并", "拆分", "抽页", "旋转"}},
		{"images-pdf", []string{"图片 pdf", "图片合成", "图片转pdf"}},
		{"config-diff", []string{"diff", "对比", "差异", "yaml"}},
		{"cron-preview", []string{"cron", "时间预览", "运行时间"}},
		{"media-info", []string{"媒体信息", "音视频信息", "音视频检查", "mediainfo"}},
		{"image-metadata", []string{"元数据", "exif", "metadata"}},
		{"qr-generate", []string{"二维码", "qrcode", "qr code"}},
		{"media-clip", []string{"截取", "剪辑", "clip"}},
	} {
		for _, word := range entry.words {
			if !strings.HasPrefix(q, "https://") && !strings.HasPrefix(q, "http://") && strings.Contains(strings.ToLower(q), word) {
				actions = append(actions, action(entry.name, "", 155))
				break
			}
		}
	}
	numeric := strings.NewReplacer("×", "*", "÷", "/", "−", "-").Replace(strings.Trim(q, "="))
	if hasDigit.MatchString(numeric) && arithmetic.MatchString(numeric) && strings.ContainsAny(text, "+*/^×÷−-") {
		a := action("calculate", text, 140)
		if value, err := calculate(text); err == nil {
			a.Title = "= " + value
			a.Detail = "计算结果 · 回车复制"
		} else {
			a.Detail = "校验并计算算式"
		}
		actions = append(actions, a)
	}
	if strings.Contains(q, "二维码") || strings.Contains(q, "扫码") || strings.Contains(strings.ToLower(q), "qrcode") || strings.Contains(strings.ToLower(q), "qr code") {
		actions = append(actions, Action{ID: "pick-image", Kind: "pick-image", Title: "选择或粘贴二维码图片", Detail: "选择图片后再执行识别", Icon: icons["qr"], Score: 140})
	}
	if p, ok := commandPayload(base64Command, text); ok {
		actions = append(actions, action("base64-encode", p, 150), action("base64-decode", p, 149))
	}
	if p, ok := commandPayload(urlCommand, text); ok {
		actions = append(actions, action("url-encode", p, 150), action("url-decode", p, 149))
	} else if percentEscape.MatchString(text) {
		actions = append(actions, action("url-decode", text, 95))
	}
	if p, ok := commandPayload(jsonCommand, text); ok {
		actions = append(actions, action("json-format", p, 150), action("json-compact", p, 149))
	} else if strings.HasPrefix(q, "{") || strings.HasPrefix(q, "[") {
		actions = append(actions, action("json-format", text, 150), action("json-compact", text, 149))
	}
	if uuidCommand.MatchString(q) {
		actions = append(actions, action("uuid", "", 150))
	}
	if p, ok := commandPayload(timestampCommand, text); ok {
		actions = append(actions, action("timestamp", p, 150))
	} else if timestampNumber.MatchString(q) || isoDate.MatchString(q) {
		actions = append(actions, action("timestamp", text, 150))
	}
	if _, err := douyinURL(q); err == nil {
		actions = append(actions, action("douyin-download", q, 185))
	}
	if _, err := bilibiliID(q); err == nil {
		actions = append(actions, action("bilibili-transcribe", q, 180))
		actions = append(actions, action("bilibili-info", q, 160))
	}
	if u, e := resourceURL(q); e == nil {
		if _, e := bilibiliID(q); e != nil {
			ext := strings.ToLower(path.Ext(u.Path))
			names := []string{"file-download", "media-transcribe", "media-audio"}
			switch ext {
			case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".tif", ".tiff", ".bmp", ".avif":
				names = append([]string{"document-text"}, names...)
				names = append(names, imageTools...)
				names = append(names, "qr-url")
			case ".pdf":
				names = append([]string{"document-text"}, names...)
			case ".mp3", ".m4a", ".aac", ".flac", ".wav", ".ogg", ".opus", ".mp4", ".mkv", ".mov", ".webm", ".m4v":
				names = []string{"media-transcribe", "media-audio", "file-download"}
			case "", ".html", ".htm", ".php", ".aspx":
				names = append([]string{"article-extract"}, names...)
			}
			for i, name := range names {
				actions = append(actions, action(name, q, 145-i))
			}
		}
	}
	// Explicit operation prefixes support signed/extensionless resource URLs.
	for _, p := range []struct{ prefix, name string }{{"download ", "file-download"}, {"下载 ", "file-download"}, {"article ", "article-extract"}, {"正文 ", "article-extract"}, {"ocr ", "document-text"}, {"转写 ", "media-transcribe"}, {"音轨 ", "media-audio"}} {
		if strings.HasPrefix(strings.ToLower(q), p.prefix) {
			raw := strings.TrimSpace(q[len(p.prefix):])
			if _, e := resourceURL(raw); e == nil {
				actions = append(actions, action(p.name, raw, 170))
			}
		}
	}
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Score > actions[j].Score })
	return actions
}
