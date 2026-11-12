package toolkit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type NotificationConfig struct{ URL, Username, Password, CAFile string }

func notifier(config NotificationConfig) (func(context.Context, Task) (string, error), error) {
	if config.URL == "" {
		return nil, nil
	}
	if !strings.HasPrefix(config.URL, "https://") || config.Username == "" || config.Password == "" {
		return nil, errors.New("ntfy requires HTTPS and authentication")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.CAFile != "" {
		ca, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(ca) {
			return nil, errors.New("invalid ntfy trust bundle")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, task Task) (string, error) {
		title, message := "✅ 工具任务完成 · "+task.Title, "结果已保存在任务页；临时文件请在有效期内下载。"
		if task.Result != nil {
			message = task.Result.Title + "\n" + message
		}
		if task.State != "succeeded" {
			title = "❌ 工具任务失败 · " + task.Title
			message = task.Error
		}
		runes := []rune(message)
		if len(runes) > 400 {
			message = string(runes[:400]) + "…"
		}
		message += "\n来源：Haven Tools"
		body, _ := json.Marshal(map[string]any{"topic": "cyberdeck-tasks", "title": title, "message": message, "priority": 3, "click": task.Href, "actions": []map[string]string{{"action": "view", "label": "查看任务结果", "url": task.Href}}})
		req, err := http.NewRequestWithContext(ctx, "POST", config.URL, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(config.Username, config.Password)
		response, err := client.Do(req)
		if err != nil {
			return "", errors.New("ntfy publish failed")
		}
		defer response.Body.Close()
		var result struct {
			ID string `json:"id"`
		}
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result) != nil || result.ID == "" {
			return "", errors.New("ntfy did not acknowledge the notification")
		}
		return result.ID, nil
	}, nil
}
