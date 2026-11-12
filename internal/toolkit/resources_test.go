package toolkit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResourceDestinationsAndSuggestions(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "::ffff:192.168.1.1", "2001:db8::1", "64:ff9b::a00:1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Fatal("accepted", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Fatal("rejected", ip)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:password@example.com/a", "http://127.0.0.1/a", "https://example.com:8443/a"} {
		if _, e := resourceURL(raw); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, item := range []struct{ input, name string }{{"https://example.com/a.pdf?sig=a%2Fb", "document-text"}, {"https://example.com/a.png", "document-text"}, {"https://example.com/a.mp4", "media-transcribe"}, {"https://example.com/a.zip", "file-download"}, {"https://example.com/post", "article-extract"}, {"ocr https://example.com/opaque?token=abc", "document-text"}} {
		a := Suggest(item.input, "text")
		if len(a) == 0 || a[0].HandlerID != item.name {
			t.Fatal(item, a)
		}
	}
	for _, a := range Suggest("", "file") {
		if a.UploadPath != "/v1/files/"+a.HandlerID || a.InputKind != "file" {
			t.Fatal(a)
		}
	}
	if actions := Suggest("", "file"); len(actions) != 6 || actions[0].HandlerID != "media-transcribe" {
		t.Fatal(actions)
	}
	// A host name resolving to loopback must fail even without a numeric URL.
	req, _ := http.NewRequest("GET", "http://localhost/secret", nil)
	if resp, e := resourceClient().Do(req); e == nil {
		resp.Body.Close()
		t.Fatal("SSRF via DNS")
	}
}
func TestTemporaryArtifactsExpiryRestartAndOwner(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	registered := map[string]AsyncTool{"fixture": {Title: "temporary", Temporary: true, Artifacts: true, Validate: func(Arguments) error { return nil }, Run: func(ctx context.Context, a Arguments, j Job) (Result, error) {
		os.WriteFile(filepath.Join(j.Dir, "file.txt"), []byte("payload"), 0600)
		if e := publish(j, "file.txt", "download", "文件.txt", "text/plain"); e != nil {
			return Result{}, e
		}
		return Result{Type: "text", Title: "result", Text: "persistent text"}, nil
	}}}
	s, e := OpenStore(root, "/tools", registered, StoreOptions{CacheDir: cache, ArtifactTTL: 60 * time.Millisecond, CleanupInterval: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.Submit("alice", "once", "fixture", Arguments{})
	if e != nil {
		t.Fatal(e)
	}
	waitTask(t, s, "alice", task.ID, "succeeded")
	task, e = s.Get("alice", task.ID)
	if e != nil || task.FilesAvailable == nil || !*task.FilesAvailable {
		t.Fatal(task, e)
	}
	service := &Service{store: s}
	get := func(who string) int {
		r := httptest.NewRequest("GET", "http://tools/", nil)
		r.SetPathValue("id", task.ID)
		r.SetPathValue("file", "file.txt")
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, who))
		w := httptest.NewRecorder()
		service.artifact(w, r)
		return w.Code
	}
	if get("alice") != 200 || get("bob") != 404 {
		t.Fatal("ownership/download")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(filepath.Join(cache, task.ID)); os.IsNotExist(e) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if get("alice") != 410 {
		t.Fatal("expired download exposed")
	}
	s.Close()
	s, e = OpenStore(root, "/tools", registered, StoreOptions{CacheDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	task, e = s.Get("alice", task.ID)
	if e != nil || task.Result.Text != "persistent text" || *task.FilesAvailable {
		t.Fatal("lost durable text", task, e)
	}
}
func TestFetcherBoundsRedirectAndArticle(t *testing.T) {
	old := fetchClient
	defer func() { fetchClient = old }()
	html := `<html><head><title>Article title</title></head><body><nav>clutter</nav><article><h1>Article title</h1>` + strings.Repeat(`<p>This is the substantive article text with enough detail to be extracted reliably. It explains a meaningful topic in complete sentences.</p>`, 12) + `</article></body></html>`
	fetchClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(html)), Request: r}, nil
	})}
	if _, e := fetchResource(context.Background(), "https://example.com/post", filepath.Join(t.TempDir(), "input"), 32); e == nil {
		t.Fatal("body limit ignored")
	}
	dir := t.TempDir()
	j := Job{Dir: dir, URL: "/tools/tasks/test", Report: func(string) error { return nil }}
	result, e := runResource(context.Background(), "article-extract", Arguments{Text: "https://example.com/post"}, j, MediaConfig{})
	if e != nil || !strings.Contains(result.Text, "substantive article") || strings.Contains(result.Text, "clutter") {
		t.Fatal(result, e)
	}
}
func TestUploadIdempotencyAndCacheOnlyInput(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	tool := AsyncTool{Title: "upload", Uploads: true, Temporary: true, Artifacts: true, Validate: func(Arguments) error { return nil }, Run: func(ctx context.Context, a Arguments, j Job) (Result, error) {
		raw, e := os.ReadFile(filepath.Join(j.Dir, "input"))
		if e != nil {
			return Result{}, e
		}
		return Result{Type: "text", Title: "processed", Text: string(raw)}, nil
	}}
	store, e := OpenStore(root, "/tools", map[string]AsyncTool{"document-text": tool}, StoreOptions{CacheDir: cache})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	service := &Service{store: store}
	submit := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://tools/v1/files/document-text", strings.NewReader(body))
		r.Header.Set("X-Request-ID", "same-upload")
		r.SetPathValue("name", "document-text")
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, "alice"))
		w := httptest.NewRecorder()
		service.upload(w, r)
		return w
	}
	first := submit("private file bytes")
	if first.Code != 202 {
		t.Fatal(first.Body.String())
	}
	var response struct {
		Task Task `json:"task"`
	}
	json.Unmarshal(first.Body.Bytes(), &response)
	waitTask(t, store, "alice", response.Task.ID, "succeeded")
	if submit("private file bytes").Code != 202 || submit("different bytes").Code != 409 {
		t.Fatal("upload idempotency failed")
	}
	raw, e := os.ReadFile(filepath.Join(root, response.Task.ID+".json"))
	if e != nil {
		t.Fatal(e)
	}
	// The extracted result is durable; the input parameter is only a fingerprint.
	var record diskTask
	json.Unmarshal(raw, &record)
	if !strings.HasPrefix(record.Arguments.Text, "upload:") || strings.Contains(record.Arguments.Text, "private file bytes") {
		t.Fatal("raw input persisted")
	}
	if _, e := store.Submit("alice", "forge", "document-text", record.Arguments); e == nil {
		t.Fatal("upload fingerprint accepted outside upload endpoint")
	}
}
