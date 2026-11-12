package toolkit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedOCRContract(t *testing.T) {
	input := filepath.Join(t.TempDir(), "scan.png")
	os.WriteFile(input, []byte("image bytes"), 0600)
	code := 200
	response := `{"text":"中文 result 123","pages":[{"page":1}],"ocrPages":1,"engine":{"engine":"RapidOCR","model":"PP-OCRv5 mobile","backend":"rocm","gpu":true}}`
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("invalid OCR request")
		}
		data, _ := io.ReadAll(r.Body)
		if string(data) != "image bytes" {
			t.Error("wrong payload")
		}
		seen = true
		w.WriteHeader(code)
		io.WriteString(w, response)
	}))
	defer server.Close()
	cfg := MediaConfig{OCRURL: server.URL, OCRToken: "test-token"}
	text, fields, err := extractDocument(context.Background(), cfg, input)
	if err != nil || text != "中文 result 123" || !strings.Contains(fields[2].Value, "GPU / rocm") || !seen {
		t.Fatal(text, fields, err)
	}
	for _, status := range []int{401, 422, 503, 500, 302} {
		code = status
		if _, _, e := extractDocument(context.Background(), cfg, input); e == nil {
			t.Fatal("accepted failure", status)
		}
	}
	code = 200
	response = `{"text":"forged","pages":[],"ocrPages":0}`
	if _, _, e := extractDocument(context.Background(), cfg, input); e == nil {
		t.Fatal("accepted invalid result")
	}
	cfg.OCRToken = ""
	if _, _, e := extractDocument(context.Background(), cfg, input); e == nil {
		t.Fatal("missing credential")
	}
}
