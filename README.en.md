# Haven Tools

[中文](README.md) · [English](README.en.md)

A searchable toolbox for the browser, self-hosted services and MCP clients. Work with PDFs, QR codes, configuration files and media locally, or send network and compute tasks to the Go service with durable job results.

## Try it online

[GitHub Pages](https://haltcatch.github.io/haven-tools/) · Seven categories of local tools; server jobs require self-hosting.

## Features

- Local file tools: merge, split, extract and rotate PDF pages, or create a PDF from images.
- Developer utilities: text/JSON/YAML diffs, timezone-aware Cron previews and QR generation.
- Media inspection: EXIF, image metadata and MediaInfo, with browser tools processing files locally.
- Server-side processing: article extraction, file downloads, QR recognition, image conversion and media processing.
- Background jobs: progress, cancellation, result downloads and optional completion notifications.
- Shared tool catalog for the web UI, HTTP API and MCP; connect separate OCR and speech-transcription services when needed.

## Quick start

Go 1.27.1, Node.js 26 and npm. Media processing additionally requires FFmpeg, yt-dlp and the relevant Python dependencies.

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

## Configuration

| Setting | Purpose |
| --- | --- |
| `LISTEN_ADDR / BASE_PATH` | Listen address and UI path; defaults: `:8080` and `/tools`. |
| `API_TOKEN / GATEWAY_KEY` | Separate random credentials for Bearer clients and a trusted gateway. |
| `PUBLIC_ORIGIN / PUBLIC_URL` | Allowed browser origin and public result URL. |
| `DATA_DIR / CACHE_DIR` | Persistent job results and temporary file directories. |
| `OCR_URL / OCR_TOKEN / WHISPER_URL` | Optional OCR and speech-transcription services. |
| `NTFY_URL` | Optional job-completion notification endpoint. |

## Usage

Open `http://localhost:8080/tools/` for the workbench. HTTP/MCP clients use the configured credentials; put an authentication gateway in front of an externally exposed deployment.

## Project structure

- `browser/` — Local operations and worker
- `internal/toolkit/` — Catalog, service endpoints and jobs
- `internal/toolkit/web/` — Workbench UI

## License

[PolyForm Noncommercial 1.0.0](LICENSE). See [LICENSE_SCOPE.md](LICENSE_SCOPE.md) for the scope of original material and third-party components, and the accompanying notices for third-party copyrights and licenses.
