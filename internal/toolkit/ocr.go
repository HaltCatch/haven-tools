package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// A trusted service URL only. User URLs are downloaded through the existing
// bounded public-resource client before any bytes reach the OCR service.
func extractDocument(ctx context.Context, config MediaConfig, input string) (string, []Field, error) {
	if config.OCRURL == "" || config.OCRToken == "" {
		return "", nil, errors.New("通用 OCR 服务尚未配置。")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	file, err := os.Open(input)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	if stat.Size() <= 0 {
		return "", nil, errors.New("OCR 文件为空。")
	}
	request, err := http.NewRequestWithContext(ctx, "POST", config.OCRURL, file)
	if err != nil {
		return "", nil, errors.New("OCR 服务地址无效。")
	}
	request.ContentLength = stat.Size()
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Authorization", "Bearer "+config.OCRToken)
	client := &http.Client{Transport: config.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "", nil, errors.New("无法连接通用 OCR 服务，请稍后重试。")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusServiceUnavailable {
		return "", nil, errors.New("OCR 服务繁忙，请稍后重试。")
	}
	if response.StatusCode != http.StatusOK {
		return "", nil, errors.New("OCR 处理失败：支持不超过 20 页的 PDF 和 3200 万像素图片；加密、损坏或不支持的文件无法处理。")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return "", nil, errors.New("OCR 服务返回内容过大。")
	}
	var result struct {
		Text  string `json:"text"`
		Pages []struct {
			Page int `json:"page"`
		} `json:"pages"`
		OCRPages int `json:"ocrPages"`
		Engine   struct {
			Engine, Model, Backend string
			GPU                    bool `json:"gpu"`
		} `json:"engine"`
	}
	if json.Unmarshal(data, &result) != nil || result.Text == "" || len(result.Text) > 650000 || len(result.Pages) < 1 || len(result.Pages) > 20 || result.OCRPages < 0 || result.OCRPages > len(result.Pages) {
		return "", nil, errors.New("OCR 服务返回无效结果。")
	}
	method := "PDF 文字层"
	if result.OCRPages > 0 {
		method = result.Engine.Engine + " · " + result.Engine.Model
		if result.Engine.GPU {
			method += " · GPU / " + result.Engine.Backend
		}
	}
	return result.Text, []Field{{"页数", fmt.Sprint(len(result.Pages))}, {"OCR 页数", fmt.Sprint(result.OCRPages)}, {"识别方式", method}}, nil
}
