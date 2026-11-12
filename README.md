# Haven Tools

[中文](README.md) · [English](README.en.md)

一个可搜索、可自托管的工具工作台。PDF、二维码、配置比较和媒体信息可以在浏览器本机处理；需要网络或计算资源的任务交给 Go 服务，并通过 HTTP 或 MCP 接入其他应用。

## 在线体验

[GitHub Pages](https://haltcatch.github.io/haven-tools/) · 浏览器本机工具可直接使用；后台任务需要自托管服务。

## 功能

- 本机文件工具：PDF 合并、拆分、抽页、旋转，图片合成 PDF。
- 开发工具：文本/JSON/YAML 差异比较、Cron 时区预览、二维码生成。
- 媒体检查：EXIF、图片元数据与 MediaInfo；浏览器工具不上传文件。
- 服务端处理：网页正文提取、文件下载、二维码识别、图片转换与音视频处理。
- 后台任务：查询进度、取消执行、下载结果，可选完成通知。
- 统一接口：浏览器工作台、HTTP API 与 MCP 共享工具目录；OCR 和语音转写通过独立服务接入。

## 快速开始

Go 1.27.1、Node.js 26，npm。媒体处理需要另外安装 FFmpeg、yt-dlp 及相应 Python 依赖。

```sh
npm ci
npm run build
export API_TOKEN="$(openssl rand -hex 32)"
export PUBLIC_ORIGIN=http://localhost:8080
export PUBLIC_URL=http://localhost:8080/tools
export DATA_DIR=./data
export CACHE_DIR=./cache
go run ./cmd/haven-tools
```

## 配置

| 配置项 | 说明 |
| --- | --- |
| `LISTEN_ADDR / BASE_PATH` | 监听地址与工具台路径，默认 `:8080`、`/tools`。 |
| `API_TOKEN / GATEWAY_KEY` | 客户端 Bearer 凭据与受信网关凭据，使用独立随机值。 |
| `PUBLIC_ORIGIN / PUBLIC_URL` | 允许的浏览器来源与结果链接地址。 |
| `DATA_DIR / CACHE_DIR` | 任务结果与临时文件目录。 |
| `OCR_URL / OCR_TOKEN / WHISPER_URL` | 可选 OCR 与语音转写服务。 |
| `NTFY_URL` | 可选任务完成通知入口。 |

## 使用

打开 `http://localhost:8080/tools/` 使用工具台。HTTP/MCP 客户端使用配置的访问凭据；对外部署时通过反向代理接入身份认证。

## 项目结构

- `browser/` — 本机处理与 Worker
- `internal/toolkit/` — 目录、服务接口与任务执行
- `internal/toolkit/web/` — 工具台界面

## 许可

[PolyForm Noncommercial 1.0.0](LICENSE)。原创部分与第三方组件的适用范围见 [LICENSE_SCOPE.md](LICENSE_SCOPE.md)；各第三方版权和许可见相应声明文件。
