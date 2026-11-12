package toolkit

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExistingToolCompatibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Text     string
		Expected *string
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		t.Run(item.Name+"/"+item.Text, func(t *testing.T) {
			result, err := ExecuteText(context.Background(), item.Name, Arguments{Text: item.Text})
			if item.Expected == nil {
				if err == nil {
					t.Fatalf("expected rejection, got %q", result.Text)
				}
				return
			}
			if err != nil || result.Text != *item.Expected {
				t.Fatalf("got %q, %v; want %q", result.Text, err, *item.Expected)
			}
		})
	}
}
func TestBoundedInputsAndDataPreservation(t *testing.T) {
	for _, text := range []string{strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), "[" + strings.Repeat("0,", 50002) + "0]"} {
		if _, err := ExecuteText(context.Background(), "json-format", Arguments{Text: text}); err == nil {
			t.Fatal("unbounded JSON accepted")
		}
	}
	for _, name := range []string{"constructor", "exec", "fetch", "qr-decode", "bilibili-info"} {
		if _, err := ExecuteText(context.Background(), name, Arguments{Text: ""}); err == nil {
			t.Fatal("unknown operation accepted", name)
		}
	}
	a, _ := ExecuteText(context.Background(), "uuid", Arguments{})
	b, _ := ExecuteText(context.Background(), "uuid", Arguments{})
	if !safeID.MatchString(a.Text) || a.Text == b.Text {
		t.Fatal("UUID generation failed")
	}
	original := strings.Repeat("你好🙂", 50000)
	encoded, err := ExecuteText(context.Background(), "base64-encode", Arguments{Text: original})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ExecuteText(context.Background(), "base64-decode", Arguments{Text: encoded.Text})
	if err != nil || decoded.Text != original {
		t.Fatalf("large UTF8 round trip: %v", err)
	}
}
func TestDeterministicSuggestions(t *testing.T) {
	for _, item := range []struct{ text, want string }{{"base64  hello ", "base64-encode"}, {"url 你好", "url-encode"}, {"{\"a\":1}", "json-format"}, {"uuid", "uuid"}, {"timestamp 0", "timestamp"}, {"(2+3)×4", "calculate"}, {"https://www.bilibili.com/video/BV1xx411c7mD", "bilibili-transcribe"}} {
		actions := Suggest(item.text, "text")
		if len(actions) == 0 || actions[0].HandlerID != item.want {
			t.Fatalf("%q: %+v", item.text, actions)
		}
	}
	actions := Suggest("base64  hello ", "text")
	if actions[0].Arguments.Text != " hello " {
		t.Fatal("payload whitespace lost")
	}
	families := map[string]bool{}
	for _, tool := range Catalog() {
		group := strings.Split(tool.Name, "-")[0]
		if tool.Editor {
			group = tool.Name
		}
		families[group] = true
	}
	if len(Suggest("工具", "text")) > 40 {
		t.Fatal("directory exceeds client contract")
	}
	if len(Suggest("工具", "text")) != len(families) {
		t.Fatal("directory must include every tool family")
	}
	if len(Suggest("帮我想一想", "text")) != 0 {
		t.Fatal("suggestion phase must not invoke an agent")
	}
	if actions = Suggest("", "image"); len(actions) != 11 || actions[0].InputKind != "image" {
		t.Fatal("image action missing")
	}
	for _, action := range Suggest("工具", "text") {
		if len(action.Icon.Paths) == 0 {
			t.Fatal("missing presentation metadata")
		}
	}
}

func qrImage(t *testing.T, text string, inverted, rotated bool) []byte {
	matrix, err := qrcode.NewQRCodeWriter().Encode(text, gozxing.BarcodeFormat_QR_CODE, 300, 300, map[gozxing.EncodeHintType]interface{}{gozxing.EncodeHintType_CHARACTER_SET: "UTF-8"})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, matrix.GetWidth(), matrix.GetHeight()))
	for y := 0; y < matrix.GetHeight(); y++ {
		for x := 0; x < matrix.GetWidth(); x++ {
			value := uint8(255)
			if matrix.Get(x, y) != inverted {
				value = 0
			}
			px, py := x, y
			if rotated {
				px, py = y, matrix.GetWidth()-x-1
			}
			img.SetGray(px, py, color.Gray{Y: value})
		}
	}
	var b bytes.Buffer
	if err = png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestRealQRCodeRecognition(t *testing.T) {
	for _, text := range []string{"你好，糖包！\n第二行 <script>不是指令</script>", "https://example.com/?a=1&b=%E4%BD%A0", "WIFI:T:WPA;S:HomeLab;P:test-password;;"} {
		for _, transform := range [][2]bool{{false, false}, {true, false}, {false, true}} {
			result, err := DecodeQR(context.Background(), qrImage(t, text, transform[0], transform[1]))
			if err != nil || result.Text != text {
				t.Fatalf("QR roundtrip %q: %q %v", text, result.Text, err)
			}
		}
	}
	if _, err := DecodeQR(context.Background(), []byte("not an image")); err == nil {
		t.Fatal("invalid image accepted")
	}
}

func waitTask(t *testing.T, s *Store, identity, id, state string) Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := s.Get(identity, id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == state {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := s.Get(identity, id)
	t.Fatalf("wanted %s, got %+v", state, task)
	return Task{}
}
func TestTasksAreDurableOwnedIdempotentAndNotReplayed(t *testing.T) {
	root := t.TempDir()
	var calls atomic.Int32
	store, err := openTestStore(root, "https://tools.example", func(ctx context.Context, args Arguments) (Result, error) {
		calls.Add(1)
		return Result{Type: "text", Title: "test", Text: args.Text}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://www.bilibili.com/video/BV1xx411c7mD"
	task, err := store.Submit("a", "same-request", "bilibili-info", Arguments{Text: url})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			other, e := store.Submit("a", "same-request", "bilibili-info", Arguments{Text: url})
			if e != nil || other.ID != task.ID {
				t.Error("idempotent submit failed", e)
			}
		})
	}
	wg.Wait()
	completed := waitTask(t, store, "a", task.ID, "succeeded")
	if completed.Result == nil || completed.Href != "https://tools.example/tasks/"+task.ID {
		t.Fatal("missing stable result")
	}
	if _, err = store.Get("b", task.ID); err != ErrMissing {
		t.Fatal("cross-owner result disclosed")
	}
	if _, err = store.Submit("a", "same-request", "bilibili-info", Arguments{Text: "https://www.bilibili.com/video/BV1ab411c7mD"}); err != ErrConflict {
		t.Fatal("changed idempotent payload accepted")
	}
	store.Close()
	if calls.Load() != 1 {
		t.Fatal("task executed more than once")
	}
	restored, err := openTestStore(root, "https://tools.example", func(context.Context, Arguments) (Result, error) { calls.Add(1); return Result{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	read, err := restored.Get("a", task.ID)
	if err != nil || read.Result == nil || read.Result.Text != url {
		t.Fatal("result did not survive restart", err)
	}
	if calls.Load() != 1 {
		t.Fatal("restart replayed completed work")
	}
}
func TestCancelAndInterruptedRecovery(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	store, err := openTestStore(root, "/tools", func(ctx context.Context, _ Arguments) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Submit("a", "cancel-me", "bilibili-info", Arguments{Text: "https://www.bilibili.com/video/BV1xx411c7mD"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = store.Cancel("a", task.ID); err != nil {
		t.Fatal(err)
	}
	waitTask(t, store, "a", task.ID, "canceled")
	store.Close()
	d, err := store.read(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.State = "running"
	if err = store.write(d); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	restored, err := openTestStore(root, "/tools", func(context.Context, Arguments) (Result, error) { calls.Add(1); return Result{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	waitTask(t, restored, "a", task.ID, "interrupted")
	if calls.Load() != 0 {
		t.Fatal("interrupted task blindly replayed")
	}
}

func testService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	s, err := NewService(Config{GatewayKey: strings.Repeat("g", 32), APIToken: strings.Repeat("a", 32), Origin: "https://homepage.example", BasePath: "/tools", PublicURL: "https://homepage.example/tools", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s)
	t.Cleanup(func() { httpServer.Close(); s.Close() })
	return s, httpServer
}
func request(t *testing.T, url, method, body, token string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func TestHTTPAuthenticationContractsAndNoFastToolPersistence(t *testing.T) {
	s, server := testService(t)
	token := strings.Repeat("a", 32)
	if got := request(t, server.URL+"/v1/catalog", "GET", "", "", nil).StatusCode; got != 401 {
		t.Fatal("anonymous access", got)
	}
	if got := request(t, server.URL+"/v1/catalog", "GET", "", token, map[string]string{"Origin": "https://evil.example"}).StatusCode; got != 403 {
		t.Fatal("cross origin", got)
	}
	response := request(t, server.URL+"/tools/v1/catalog", "GET", "", token, nil)
	var catalog map[string]any
	json.NewDecoder(response.Body).Decode(&catalog)
	if len(catalog["tools"].([]any)) != len(Catalog()) {
		t.Fatal("catalog missing tools")
	}
	for _, body := range []string{`{"protocolVersion":1,"name":"calculate","arguments":{"text":"1+1","url":"http://127.0.0.1"}}`, `{"protocolVersion":1,"name":"calculate","arguments":{"text":null}}`, `{"protocolVersion":1,"name":"calculate","arguments":{"text":"1+1"},"script":"exec"}`, `{"protocolVersion":1,"name":"calculate","arguments":{"text":"1+1"}} {}`} {
		if response = request(t, server.URL+"/v1/execute", "POST", body, token, nil); response.StatusCode != 400 {
			t.Fatal("invalid invocation accepted", body, response.StatusCode)
		}
	}
	response = request(t, server.URL+"/v1/execute", "POST", `{"protocolVersion":1,"name":"calculate","arguments":{"text":"(2+3)×4"}}`, token, nil)
	var data struct{ Result Result }
	json.NewDecoder(response.Body).Decode(&data)
	if response.StatusCode != 200 || data.Result.Text != "20" {
		t.Fatal("HTTP execution failed", response.StatusCode, data)
	}
	entries, _ := os.ReadDir(s.store.root)
	if len(entries) != 0 {
		t.Fatal("fast tool content was persisted")
	}
	response = request(t, server.URL+"/v1/suggest", "POST", `{"protocolVersion":1,"inputKind":"text","text":"1+1","resolver":"model"}`, token, nil)
	if response.StatusCode != 400 {
		t.Fatal("model unexpectedly enabled")
	}
	response = request(t, server.URL+"/tools/tasks", "GET", "", token, nil)
	html, _ := io.ReadAll(response.Body)
	if !bytes.Contains(html, []byte("/tools/assets/app.js")) || bytes.Contains(html, []byte("__TOOLS_BASE__")) {
		t.Fatal("independent UI base path failed")
	}
}

func TestOfficialMCPClientCallsSameEngine(t *testing.T) {
	_, server := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: authTransport{http.DefaultTransport}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	expected := 3
	for _, tool := range Catalog() {
		if tool.Mode != "browser" && tool.InputKind == "text" {
			expected++
		}
	}
	if err != nil || len(tools.Tools) != expected {
		t.Fatal("MCP discovery failed", err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "calculate", Arguments: map[string]any{"text": "(2+3)×4"}})
	if err != nil || result.IsError || result.Content[0].(*mcp.TextContent).Text != "20" {
		t.Fatal("MCP deterministic calculation failed", err, result)
	}
	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "calculate", Arguments: map[string]any{"text": "fetch(1)"}})
	if err != nil || !bad.IsError {
		t.Fatal("MCP must reject executable code")
	}
}

type authTransport struct{ base http.RoundTripper }

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	return t.base.RoundTrip(clone)
}

func BenchmarkCalculate(b *testing.B) {
	for b.Loop() {
		ExecuteText(context.Background(), "calculate", Arguments{Text: "(2+3)×4"})
	}
}
func BenchmarkSuggest(b *testing.B) {
	for b.Loop() {
		Suggest("base64 你好，世界", "text")
	}
}
func BenchmarkJSON(b *testing.B) {
	text := `{"n":900719925474099312345,"a":[1,2,3]}`
	for b.Loop() {
		ExecuteText(context.Background(), "json-format", Arguments{Text: text})
	}
}

func TestStateFileDoesNotExposePrivateTaskFields(t *testing.T) {
	root := t.TempDir()
	id, _ := newID()
	s := &Store{root: root}
	d := diskTask{Task: Task{ID: id, State: "queued"}, Owner: "private-owner", RequestID: "request", Arguments: Arguments{Text: "private-input"}}
	if err := s.write(d); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, id+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state permissions", err)
	}
	raw, _ := json.Marshal(d.Task)
	if bytes.Contains(raw, []byte("private-owner")) || bytes.Contains(raw, []byte("private-input")) {
		t.Fatal("internal fields leaked into API task")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBilibiliUsesOnlyFixedAPIAndRejectsUnsafeRedirects(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.bilibili.com" || r.URL.Path != "/x/web-interface/view" {
			t.Fatal("unexpected network destination", r.URL)
		}
		body := `{"code":0,"data":{"bvid":"BV1xx411c7mD","title":"<script>sample</script>","desc":"description","duration":65,"pubdate":1700000000,"owner":{"name":"author"}}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	result, err := ReadBilibili(context.Background(), "https://www.bilibili.com/video/BV1xx411c7mD", transport)
	if err != nil || result.Type != "fields" || len(result.Fields) != 4 || result.Title != "<script>sample</script>" || calls != 1 {
		t.Fatal(result, err, calls)
	}
	for _, text := range []string{"https://127.0.0.1/video/BV1xx411c7mD", "https://www.bilibili.com.evil.test/video/BV1xx411c7mD", "https://user@bilibili.com/video/BV1xx411c7mD", "http://bilibili.com/video/BV1xx411c7mD", "https://bilibili.com:443/video/BV1xx411c7mD"} {
		if _, err := ReadBilibili(context.Background(), text, transport); err == nil {
			t.Fatal("accepted unsafe input", text)
		}
	}
	if calls != 1 {
		t.Fatal("invalid input performed a request")
	}
	redirects := 0
	badRedirect := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		redirects++
		if r.URL.Host != "b23.tv" {
			t.Fatal("followed redirect", r.URL)
		}
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{"https://127.0.0.1/private"}}, Request: r}, nil
	})
	if _, err := ReadBilibili(context.Background(), "https://b23.tv/example", badRedirect); err == nil || redirects != 1 {
		t.Fatal("unsafe short link", err, redirects)
	}
}
func TestStateFailureMakesServiceUnreadyWithoutClaimingCompletion(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(Config{GatewayKey: strings.Repeat("g", 32), APIToken: strings.Repeat("a", 32), DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	// Replace the storage directory with a regular file, including on root-run CI.
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(root, []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(root)
	if _, err = service.store.Submit("user:test", "failure", "bilibili-info", Arguments{Text: "https://www.bilibili.com/video/BV1xx411c7mD"}); err != ErrStorage {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, httptest.NewRequest("GET", "/health", nil))
	if recorder.Code != 503 {
		t.Fatal("readiness claimed writable state", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	service.ServeHTTP(recorder, httptest.NewRequest("GET", "/live", nil))
	if recorder.Code != 200 {
		t.Fatal("liveness should not restart a storage failure")
	}
}

func TestLargeHighBitDepthPNGIsRejectedBeforePixelAllocation(t *testing.T) {
	raw := []byte("\x89PNG\r\n\x1a\n")
	chunk := make([]byte, 25)
	binary.BigEndian.PutUint32(chunk, 13)
	copy(chunk[4:], "IHDR")
	binary.BigEndian.PutUint32(chunk[8:], 5000)
	binary.BigEndian.PutUint32(chunk[12:], 5000)
	chunk[16], chunk[17] = 16, 6 // 25MP, 16-bit RGBA: 200MB decoded before QR buffers.
	binary.BigEndian.PutUint32(chunk[21:], crc32.ChecksumIEEE(chunk[4:21]))
	raw = append(raw, chunk...)
	if _, err := DecodeQR(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "内存") {
		t.Fatal("must reject decoded memory budget before image decoding", err)
	}
}

func openTestStore(root, base string, handler Handler) (*Store, error) {
	task := bilibiliTask()
	task.Execute = handler
	return OpenStore(root, base, map[string]AsyncTool{"bilibili-info": task})
}
func TestDurableStoreDispatchesIndependentAsyncTools(t *testing.T) {
	executed := make(chan string, 2)
	tools := map[string]AsyncTool{}
	for _, name := range []string{"first", "second"} {
		tools[name] = AsyncTool{Title: name, Validate: func(args Arguments) error {
			if args.Text != "input" {
				return errors.New("invalid input")
			}
			return nil
		}, Execute: func(ctx context.Context, args Arguments) (Result, error) {
			executed <- name
			return Result{Type: "text", Title: name, Text: name + ":" + args.Text}, nil
		}}
	}
	store, err := OpenStore(t.TempDir(), "/results", tools)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A caller's later registry mutation cannot change a running service.
	delete(tools, "first")
	for _, name := range []string{"first", "second"} {
		task, err := store.Submit("owner", name, name, Arguments{Text: "input"})
		if err != nil {
			t.Fatal(err)
		}
		result := waitTask(t, store, "owner", task.ID, "succeeded")
		if result.Title != name || result.Result.Text != name+":input" {
			t.Fatal(result)
		}
	}
	if <-executed != "first" || <-executed != "second" {
		t.Fatal("wrong async dispatch")
	}
	if _, err := store.Submit("owner", "bad", "first", Arguments{Text: "invalid"}); err == nil {
		t.Fatal("tool validator was bypassed")
	}
	if len(store.List("owner")) != 2 {
		t.Fatal("invalid input must not create task state")
	}
}
