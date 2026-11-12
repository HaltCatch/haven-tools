package toolkit

import "strings"

var icons = map[string]Icon{
	"download":    {"download", []string{"M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5"}},
	"article":     {"article", []string{"M5 3h10l4 4v14H5zM14 3v5h5M8 12h8M8 16h8"}},
	"image":       {"image", []string{"M3 3h18v18H3zM3 17l6-6 4 4 3-3 5 5M15 7h1"}},
	"calculator":  {"calculator", []string{"M5 3h14v18H5zM8 6h8M8 11h1M12 11h1M16 11h1M8 15h1M12 15h1M16 15h1M8 18h1M12 18h1M16 18h1"}},
	"binary":      {"binary", []string{"M4 4h4v6H4zM4 14h4v6H4zM16 4h2v6M14 10h6M16 14h2v6M14 20h6"}},
	"link":        {"link", []string{"m10 13 4-4M8 16l-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0"}},
	"braces":      {"braces", []string{"M8 3H6v6l-3 3 3 3v6h2M16 3h2v6l3 3-3 3v6h-2M10 9h4M10 15h4"}},
	"fingerprint": {"fingerprint", []string{"M8 9a4 4 0 0 1 8 0v5M5 12V9a7 7 0 0 1 14 0v4M11 8h2v6a9 9 0 0 1-3 7M8 13v1a6 6 0 0 1-2 5M16 17a12 12 0 0 1-2 5"}},
	"clock":       {"clock", []string{"M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0M12 7v5l3 2"}},
	"qr":          {"qr", []string{"M3 3h6v6H3zM15 3h6v6h-6zM3 15h6v6H3zM15 15h3v3h3M15 21h3M21 12v3M12 3v3M12 12h3M3 12h3M12 18v3"}},
	"video":       {"video", []string{"M3 5h13v14H3zM16 10l5-3v10l-5-3M7 8l5 4-5 4z"}},
	"tasks":       {"tasks", []string{"m3 6 2 2 3-4M11 6h10m-18 8 2 2 3-4M11 14h10M11 20h10"}},
}

func makeTool(name, title, description, category, icon, example string) Tool {
	return Tool{Name: name, Title: title, Description: description, Category: category, Icon: icons[icon], Mode: "sync", InputKind: "text", Example: example,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "maxLength": MaxText}}, "required": []string{"text"}, "additionalProperties": false}}
}

func buildCatalog() []Tool {
	tools := []Tool{
		makeTool("calculate", "计算结果", "计算四则、乘方和括号算式。", "计算", "calculator", "(2+3)×4"),
		makeTool("base64-encode", "Base64 编码（UTF-8）", "编码原始文本，保留空白与换行。", "编码", "binary", "base64 你好，世界"),
		makeTool("base64-decode", "Base64 解码（UTF-8）", "严格校验 Base64 与 UTF-8。", "编码", "binary", "base64 SGVsbG8="),
		makeTool("url-encode", "URL 参数编码", "编码 URL 组件中的特殊字符。", "编码", "link", "url 你好 world"),
		makeTool("url-decode", "URL 解码", "解码 URL 组件，加号保持原样。", "编码", "link", "url %E4%BD%A0"),
		makeTool("json-format", "JSON 格式化", "保留大整数、重复键与数字原文。", "数据", "braces", "json {\"hello\":\"world\"}"),
		makeTool("json-compact", "JSON 压缩", "只移除字符串外部的空白。", "数据", "braces", "json { \"hello\": \"world\" }"),
		makeTool("uuid", "生成 UUID v4", "生成一个随机标识。", "数据", "fingerprint", "uuid"),
		makeTool("timestamp", "时间戳 / 日期转换", "转换秒、毫秒与带时区日期。", "时间", "clock", "timestamp now"),
		makeTool("qr-decode", "识别二维码", "选择后上传图片识别，不自动打开内容。", "图像", "qr", ""),
		makeTool("bilibili-info", "读取 B 站视频信息", "后台读取标题、作者、时长与简介，完成后保留结果。", "链接", "video", "https://www.bilibili.com/video/BV1xx411c7mD"),
		makeTool("bilibili-transcribe", "下载 B 站视频并转文字", "下载视频 → GPU 语音转写 → 保存结果与下载链接 → 完成通知。支持公开单视频 / 指定分 P，自动处理长视频。", "链接", "video", "https://www.bilibili.com/video/BV16QGvzmEen"),
	}
	tools[9].InputKind = "image"
	tools[10].Mode = "async"
	tools[11].Mode = "async"
	for _, spec := range []struct{ name, title, description, category, icon, example string }{
		{"douyin-download", "抖音无水印视频下载", "粘贴分享链接或整段文案，解析公开单视频并生成下载按钮。", "链接", "video", "粘贴抖音分享链接"},
		{"file-download", "临时下载文件", "下载公开文件到临时目录，24 小时自动清理，不设固定文件大小上限。", "文件", "download", "https://example.com/file.zip"},
		{"article-extract", "提取网页正文", "Readability 提取标题与正文，导出 Markdown；不执行网页脚本。", "网页", "article", "https://go.dev/blog/go1.25"},
		{"document-text", "PDF / 图片提取文字", "PDF 文字层优先，扫描页和图片使用中英文 OCR；最多 20 页，不设固定文件大小上限。", "文档", "article", "https://example.com/document.pdf"},
		{"media-transcribe", "音视频转文字（链接 / 上传）", "自动识别文件内容、分段调用 GPU Whisper，无需手工转换格式。", "音视频", "video", "https://example.com/audio.mp3"},
		{"media-audio", "提取音视频中的音轨", "链接或上传，FFmpeg 自动识别格式并提取完整音轨为 M4A。", "音视频", "video", "https://example.com/video.mp4"},
		{"qr-url", "识别链接图片中的二维码", "下载公开图片后识别，不自动打开内容。", "图像", "qr", "https://example.com/qr.png"},
		{"image-compress", "压缩图片为 WebP", "Pillow 按质量 80 压缩，显示尺寸和结果大小。", "图像", "image", "https://example.com/image.png"},
		{"image-resize", "缩小图片至 1920 像素", "长边最多 1920，保持比例，不放大，输出 WebP。", "图像", "image", "https://example.com/image.jpg"},
		{"image-png", "图片转 PNG", "保留透明背景，移除 EXIF 信息。", "图像", "image", "https://example.com/image.webp"},
		{"image-jpeg", "图片转 JPEG", "透明背景填白，质量 90，移除 EXIF 信息。", "图像", "image", "https://example.com/image.png"},
		{"image-webp", "图片转 WebP", "保留透明背景，质量 90，移除 EXIF 信息。", "图像", "image", "https://example.com/image.png"},
	} {
		t := makeTool(spec.name, spec.title, spec.description, spec.category, spec.icon, spec.example)
		t.Mode = "async"
		tools = append(tools, t)
	}

	for _, spec := range []struct{ name, title, description, category, icon, example string }{
		{"pdf-workbench", "PDF 合并 / 拆分 / 抽页 / 旋转 / 信息", "本机处理 PDF；可查看页数、尺寸与加密状态，不上传文件。", "文档", "article", "pdf"},
		{"images-pdf", "图片合成 PDF", "选择多张图片、调整顺序，每张一页，本机生成 PDF。", "文档", "image", "图片 PDF"},
		{"config-diff", "文本 / JSON / YAML 差异比较", "比较两份配置，显示修改部分；结构比较忽略对象键顺序，保留整数精度。", "数据", "braces", "对比"},
		{"cron-preview", "Cron 下次运行时间预览", "选择时区与起算时间，预览后续 10 次执行时间，不创建任务。", "时间", "clock", "cron"},
		{"media-info", "音视频信息检查（本地）", "MediaInfo 按需读取文件片段，无需上传大视频；查看编码、时长与轨道。", "音视频", "video", "媒体信息"},
		{"image-metadata", "图片元数据查看（本地）", "查看 EXIF、拍摄信息与 GPS 等元数据；文件不上传。", "图像", "image", "元数据"},
		{"qr-generate", "生成二维码", "本地生成 PNG / SVG；不自动打开内容。", "图像", "qr", "生成二维码"},
		{"media-clip", "音视频截取", "上传或公开直链，快速关键帧截取或精确重编码，后台生成下载文件。", "音视频", "video", "截取"},
	} {
		t := makeTool(spec.name, spec.title, spec.description, spec.category, spec.icon, spec.example)
		t.Editor = true
		t.Mode = "browser"
		if t.Name == "media-clip" {
			t.Mode = "async"
			t.InputSchema["properties"].(map[string]any)["clip"] = clipSchema
			t.InputSchema["required"] = []string{"text", "clip"}
		}
		tools = append(tools, t)
	}
	return tools
}

var tools = buildCatalog()

func Catalog() []Tool { return append([]Tool(nil), tools...) }
func findTool(name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}
func resultFor(name, text string) Result {
	t, _ := findTool(name)
	kind := "text"
	if strings.HasPrefix(name, "json-") {
		kind = "json"
	}
	return Result{Type: kind, Title: t.Title, Text: text, Icon: t.Icon, CopyOnSelect: name == "calculate"}
}
func action(name, text string, score int) Action {
	t, _ := findTool(name)
	return Action{Editor: t.Editor, ID: name, Kind: "remote", Title: t.Title, Detail: t.Description, Icon: t.Icon, Score: score, HandlerID: name, Arguments: &Arguments{Text: text}, InputKind: t.InputKind}
}
