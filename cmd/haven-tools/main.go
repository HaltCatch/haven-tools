package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaltCatch/haven-tools/internal/toolkit"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	service, err := toolkit.NewService(toolkit.Config{GatewayKey: os.Getenv("GATEWAY_KEY"), APIToken: os.Getenv("API_TOKEN"), Origin: env("PUBLIC_ORIGIN", "https://service.example.com"), BasePath: env("BASE_PATH", "/tools"), PublicURL: env("PUBLIC_URL", "https://service.example.com/tools"), DataDir: env("DATA_DIR", "/data"), CacheDir: env("CACHE_DIR", "/cache"),
		Media:        toolkit.MediaConfig{WhisperURL: os.Getenv("WHISPER_URL"), OCRURL: os.Getenv("OCR_URL"), OCRToken: os.Getenv("OCR_TOKEN"), Python: env("MEDIA_PYTHON", "python3"), Downloader: os.Getenv("MEDIA_DOWNLOADER"), Processor: os.Getenv("FILE_PROCESSOR")},
		Notification: toolkit.NotificationConfig{URL: os.Getenv("NTFY_URL"), Username: os.Getenv("NTFY_USERNAME"), Password: os.Getenv("NTFY_PASSWORD"), CAFile: os.Getenv("NTFY_CA_FILE")},
	})
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	server := toolkit.Server(env("LISTEN_ADDR", ":8080"), service)
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		slog.Info("ready", "version", toolkit.Version)
		if err := server.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			slog.Error("http server stopped", "error", err)
			done <- syscall.SIGTERM
		}
	}()
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	service.Close()
}
