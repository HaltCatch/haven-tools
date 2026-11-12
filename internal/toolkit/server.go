package toolkit

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed web/*
var web embed.FS

type Config struct {
	GatewayKey, APIToken, Origin, BasePath, PublicURL, DataDir string
	CacheDir                                                   string
	ArtifactTTL, CleanupInterval                               time.Duration
	Media                                                      MediaConfig
	Notification                                               NotificationConfig
}
type identityKey struct{}

func owner(ctx context.Context) string { value, _ := ctx.Value(identityKey{}).(string); return value }

type Service struct {
	config Config
	store  *Store
	slots  chan struct{}
	images chan struct{}
	mux    *http.ServeMux
}

func NewService(config Config) (*Service, error) {
	if len(config.GatewayKey) < 32 || len(config.APIToken) < 32 || equal(config.GatewayKey, config.APIToken) {
		return nil, errors.New("two backend credentials of at least 32 bytes are required")
	}
	if config.BasePath != "" && (!strings.HasPrefix(config.BasePath, "/") || strings.ContainsAny(config.BasePath, "<>\"'?#\\ ")) {
		return nil, errors.New("invalid base path")
	}
	s := &Service{config: config, slots: make(chan struct{}, 4), images: make(chan struct{}, 1), mux: http.NewServeMux()}
	registered := resourceTasks(config.Media)
	registered["bilibili-info"] = bilibiliTask()
	registered["douyin-download"] = douyinTask()
	registered["bilibili-transcribe"] = mediaTask(config.Media)
	store, err := OpenStore(config.DataDir, config.PublicURL, registered, StoreOptions{CacheDir: config.CacheDir, ArtifactTTL: config.ArtifactTTL, CleanupInterval: config.CleanupInterval})
	if err != nil {
		return nil, err
	}
	s.store = store
	store.notify, err = notifier(config.Notification)
	if err != nil {
		store.Close()
		return nil, err
	}
	s.mux.HandleFunc("GET /v1/catalog", func(w http.ResponseWriter, r *http.Request) {
		s.send(w, 200, map[string]any{"tools": Catalog(), "resolvers": []string{"rules"}, "views": []string{"text", "json", "fields"}, "version": Version})
	})
	s.mux.HandleFunc("POST /v1/suggest", s.suggest)
	s.mux.HandleFunc("POST /v1/execute", s.execute)
	s.mux.HandleFunc("POST /v1/image", s.image)
	s.mux.HandleFunc("POST /v1/files/{name}", s.upload)
	s.mux.HandleFunc("GET /v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		s.send(w, 200, map[string]any{"tasks": s.store.List(owner(r.Context()))})
	})
	s.mux.HandleFunc("GET /v1/tasks/{id}", s.task)
	s.mux.HandleFunc("POST /v1/tasks/{id}/cancel", s.task)
	s.mux.HandleFunc("GET /v1/tasks/{id}/files/{file}", s.artifact)
	s.mux.HandleFunc("GET /tasks/{id}/files/{file}", s.artifact)
	mcpHandler := s.mcpHandler()
	for _, method := range []string{"GET", "POST", "DELETE"} {
		s.mux.Handle(method+" /mcp", mcpHandler)
	}
	assets, _ := fs.Sub(web, "web")
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	s.mux.HandleFunc("GET /", s.page)
	return s, nil
}

var Version = "dev"

func (s *Service) Close()    { s.store.Close() }
func equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; worker-src 'self'; style-src 'self'; img-src 'self' blob: data:; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
	path := r.URL.Path
	if s.config.BasePath != "" && (path == s.config.BasePath || strings.HasPrefix(path, s.config.BasePath+"/")) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Path = strings.TrimPrefix(path, s.config.BasePath)
		if u.Path == "" {
			u.Path = "/"
		}
		clone.URL = &u
		r = clone
	}
	if r.URL.Path == "/live" {
		s.send(w, 200, map[string]any{"ok": true})
		return
	}
	if r.URL.Path == "/health" {
		if s.store.Healthy() != nil {
			s.send(w, 503, map[string]any{"error": "任务存储不可用。"})
			return
		}
		s.send(w, 200, map[string]any{"ok": true, "version": Version})
		return
	}
	origin := r.Header.Get("Origin")
	// Notification apps and external sites legitimately navigate to a result
	// document. Only safe top-level page navigation is exempt; API, media,
	// embedded documents and mutations keep the strict same-origin boundary.
	navigation := (r.Method == "GET" || r.Method == "HEAD") && pagePath(r.URL.Path) &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if (origin != "" && origin != s.config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site") && !navigation {
		s.send(w, 403, map[string]any{"error": "来源不允许。"})
		return
	}
	identity := ""
	if equal(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.config.APIToken) {
		identity = "api"
	} else if equal(r.Header.Get("X-Haven-Tools-Key"), s.config.GatewayKey) && r.Header.Get("X-Authentik-Uid") != "" {
		identity = "user:" + r.Header.Get("X-Authentik-Uid")
	}
	if identity == "" {
		s.send(w, 401, map[string]any{"error": "需要有效的服务端身份。"})
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
	if strings.HasPrefix(r.URL.Path, "/v1/tasks") && s.store.Healthy() != nil {
		s.send(w, 503, map[string]any{"error": ErrStorage.Error()})
		return
	}
	if r.Method == "POST" {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			w.Header().Set("Retry-After", "1")
			s.send(w, 429, map[string]any{"error": ErrBusy.Error()})
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func pagePath(path string) bool {
	return path == "/" || path == "/tasks" || (strings.HasPrefix(path, "/tasks/") && safeID.MatchString(strings.TrimPrefix(path, "/tasks/")))
}

func (s *Service) send(w http.ResponseWriter, status int, data map[string]any) {
	data["protocolVersion"] = ProtocolVersion
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
func (s *Service) body(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("请使用 JSON 请求。")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6_000_000)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return errors.New("输入内容过大或读取失败。")
	}
	if err = decodeStrict(data, target); err != nil {
		return errors.New("请求格式或参数无效。")
	}
	return nil
}
func (s *Service) suggest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProtocolVersion int    `json:"protocolVersion"`
		Text            string `json:"text"`
		InputKind       string `json:"inputKind"`
		Resolver        string `json:"resolver"`
	}
	if err := s.body(w, r, &body); err != nil {
		s.send(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if body.ProtocolVersion != ProtocolVersion {
		s.send(w, 409, map[string]any{"error": "工具版本已更新，请刷新页面。"})
		return
	}
	if body.Resolver != "" && body.Resolver != "rules" || len(body.Text) > MaxText*4 || (body.InputKind != "text" && body.InputKind != "image" && body.InputKind != "file") {
		s.send(w, 400, map[string]any{"error": "输入无效或未启用该建议提供方。"})
		return
	}
	actions := Suggest(body.Text, body.InputKind)
	s.send(w, 200, map[string]any{"resolver": "rules", "actions": actions})
}
func (s *Service) execute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProtocolVersion int             `json:"protocolVersion"`
		Name            string          `json:"name"`
		Arguments       json.RawMessage `json:"arguments"`
		RequestID       string          `json:"requestId"`
	}
	if err := s.body(w, r, &body); err != nil {
		s.send(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if body.ProtocolVersion != ProtocolVersion {
		s.send(w, 409, map[string]any{"error": "工具版本已更新，请刷新页面。"})
		return
	}
	args, err := decodeArguments(body.Arguments)
	if err != nil {
		s.send(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if tool, ok := findTool(body.Name); ok && tool.Mode == "async" {
		task, e := s.store.Submit(owner(r.Context()), body.RequestID, body.Name, args)
		if e != nil {
			s.taskError(w, e)
			return
		}
		s.send(w, 202, map[string]any{"task": task})
		return
	}
	result, err := ExecuteText(r.Context(), body.Name, args)
	if err != nil {
		s.send(w, 422, map[string]any{"error": err.Error()})
		return
	}
	s.send(w, 200, map[string]any{"result": result})
}
func (s *Service) image(w http.ResponseWriter, r *http.Request) {
	select {
	case s.images <- struct{}{}:
		defer func() { <-s.images }()
	default:
		s.send(w, 429, map[string]any{"error": ErrBusy.Error()})
		return
	}
	defer r.Body.Close()
	if err := os.MkdirAll(s.store.cacheRoot, 0700); err != nil {
		s.taskError(w, ErrStorage)
		return
	}
	file, err := os.CreateTemp(s.store.cacheRoot, ".qr-*")
	if err != nil {
		s.taskError(w, ErrStorage)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	controller := http.NewResponseController(w)
	controller.SetWriteDeadline(time.Time{})
	n, err := copyToDisk(file, uploadReader{r.Body, controller})
	if errors.Is(err, errDiskSpace) {
		s.send(w, 507, map[string]any{"error": errDiskSpace.Error()})
		return
	}
	if err != nil {
		s.send(w, 400, map[string]any{"error": "上传中断或临时存储不可用。"})
		return
	}
	if n == 0 {
		s.send(w, 400, map[string]any{"error": "文件为空。"})
		return
	}
	file.Seek(0, io.SeekStart)
	result, err := decodeQR(r.Context(), file)
	if err != nil {
		s.send(w, 422, map[string]any{"error": err.Error()})
		return
	}
	s.send(w, 200, map[string]any{"result": result})
}
func (s *Service) taskError(w http.ResponseWriter, err error) {
	status := 400
	if errors.Is(err, ErrBusy) {
		status = 429
	}
	if errors.Is(err, ErrConflict) {
		status = 409
	}
	if errors.Is(err, ErrMissing) {
		status = 404
	}
	if errors.Is(err, ErrStorage) {
		status = 503
	}
	s.send(w, status, map[string]any{"error": err.Error()})
}
func (s *Service) task(w http.ResponseWriter, r *http.Request) {
	var task Task
	var err error
	if r.Method == "POST" {
		task, err = s.store.Cancel(owner(r.Context()), r.PathValue("id"))
	} else {
		task, err = s.store.Get(owner(r.Context()), r.PathValue("id"))
	}
	if err != nil {
		s.taskError(w, err)
		return
	}
	s.send(w, 200, map[string]any{"task": task})
}
func (s *Service) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/tasks" && !strings.HasPrefix(r.URL.Path, "/tasks/") {
		http.NotFound(w, r)
		return
	}
	data, _ := web.ReadFile("web/index.html")
	html := strings.ReplaceAll(string(data), "__TOOLS_BASE__", s.config.BasePath)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func Server(address string, service *Service) *http.Server {
	return &http.Server{Addr: address, Handler: service, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
}
