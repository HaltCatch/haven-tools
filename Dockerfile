FROM node:22.22.3-alpine@sha256:e58326d0d441090181ac150dc2078d3e2cf6a0d42e809aebba3ef5880935ffdd AS web
WORKDIR /web
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts --no-audit --no-fund
COPY browser ./browser
COPY scripts/build-web.mjs ./scripts/build-web.mjs
RUN mkdir -p internal/toolkit/web && npm run build
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
ENV GOMAXPROCS=2
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /web/internal/toolkit/web/ ./internal/toolkit/web/
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -p=1 -trimpath -ldflags="-s -w -X github.com/HaltCatch/haven-tools/internal/toolkit.Version=${VERSION}" -o /haven-tools ./cmd/haven-tools
FROM python:3.13.17-alpine3.23@sha256:df8241c52fff8aefebb789ce9e140fb20e9f1f1ff9e5bca28b2064831eb6ff04
# yt-dlp runs only during Bilibili tasks. FFmpeg probes/extracts audio and
# segments normalized WAV; the existing Whisper service owns GPU inference.
RUN --mount=type=cache,target=/var/cache/apk apk add --cache-dir /var/cache/apk ffmpeg=8.0.1-r1
COPY media/requirements.txt /opt/haven-tools/requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip pip install --require-hashes --no-deps -r /opt/haven-tools/requirements.txt
COPY media/download.py media/process.py /opt/haven-tools/
COPY media/test_process.py /opt/haven-tools/test_process.py
RUN python3 -m unittest discover -s /opt/haven-tools -p test_process.py
COPY --from=build /haven-tools /haven-tools
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY licenses /licenses
USER 65532:65532
ENV PYTHONDONTWRITEBYTECODE=1
EXPOSE 8080
ENTRYPOINT ["/haven-tools"]
