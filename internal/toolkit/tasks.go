package toolkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Task struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	Href           string     `json:"href"`
	Error          string     `json:"error,omitempty"`
	Result         *Result    `json:"result,omitempty"`
	Stage          string     `json:"stage,omitempty"`
	Notice         *Notice    `json:"notification,omitempty"`
	Files          []Artifact `json:"files,omitempty"`
	FilesExpireAt  *time.Time `json:"filesExpireAt,omitempty"`
	FilesAvailable *bool      `json:"filesAvailable,omitempty"`
}

type Notice struct {
	State string `json:"state"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// Job supplies task-scoped artifacts and infrequent durable phase updates.
// A client disconnect cancels polling, not this execution context.
type Job struct {
	ID, Dir, URL string
	Report       func(string) error
	Publish      func(Artifact) error
}
type diskTask struct {
	Task
	Owner     string    `json:"owner"`
	RequestID string    `json:"requestId"`
	Arguments Arguments `json:"arguments"`
}
type taskRecord struct {
	task                          Task
	owner, requestID, fingerprint string
}

// AsyncTool binds ordinary Go validation/execution to the durable task engine.
// The store has no knowledge of URLs, Bilibili or any other tool's business rules.
type AsyncTool struct {
	Title     string
	Validate  func(Arguments) error
	Execute   Handler
	Run       func(context.Context, Arguments, Job) (Result, error)
	Timeout   time.Duration // Zero selects the default; negative allows explicit cancellation only.
	Artifacts bool
	Notify    bool
	Temporary bool
	Uploads   bool
}
type StoreOptions struct {
	CacheDir                     string
	ArtifactTTL, CleanupInterval time.Duration
}
type Store struct {
	mu                           sync.Mutex
	root, base                   string
	records                      map[string]taskRecord
	requests                     map[string]string
	queue                        chan string
	tools                        map[string]AsyncTool
	cancels                      map[string]context.CancelFunc
	ctx                          context.Context
	cancel                       context.CancelFunc
	done                         chan struct{}
	storageErr                   error
	notify                       func(context.Context, Task) (string, error)
	cacheRoot                    string
	artifactTTL, cleanupInterval time.Duration
}

var safeID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var safeRequest = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
var ErrConflict = errors.New("同一请求编号不能用于不同内容。")
var ErrMissing = errors.New("任务不存在或无权查看。")
var ErrStorage = errors.New("任务存储不可用，请稍后重试。")

func taskKey(owner, request string) string {
	hash := sha256.Sum256([]byte(owner + "\x00" + request))
	return hex.EncodeToString(hash[:])
}
func fingerprint(name string, args Arguments) string {
	raw, _ := json.Marshal(args)
	return taskKey(name, string(raw))
}
func terminal(state string) bool { return state != "queued" && state != "running" }

func OpenStore(root, base string, tools map[string]AsyncTool, options ...StoreOptions) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	registered := make(map[string]AsyncTool, len(tools))
	for name, tool := range tools {
		if name == "" || tool.Title == "" || (tool.Execute == nil && tool.Run == nil) || tool.Validate == nil {
			return nil, errors.New("invalid async tool registration")
		}
		registered[name] = tool
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{root: root, base: strings.TrimRight(base, "/"), records: map[string]taskRecord{}, requests: map[string]string{}, queue: make(chan string, 16), tools: registered, cancels: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.cacheRoot, s.artifactTTL, s.cleanupInterval = filepath.Join(root, ".cache"), 24*time.Hour, 10*time.Minute
	if len(options) > 0 {
		o := options[0]
		if o.CacheDir != "" {
			s.cacheRoot = o.CacheDir
		}
		if o.ArtifactTTL > 0 {
			s.artifactTTL = o.ArtifactTTL
		}
		if o.CleanupInterval > 0 {
			s.cleanupInterval = o.CleanupInterval
		}
	}
	if filepath.Clean(s.cacheRoot) == filepath.Clean(root) {
		cancel()
		return nil, errors.New("cache and durable task directories must differ")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !safeID.MatchString(id) {
			continue
		}
		d, e := s.read(id)
		if e != nil || d.ID != id || d.Owner == "" || !safeRequest.MatchString(d.RequestID) {
			cancel()
			return nil, fmt.Errorf("invalid task state %s", id)
		}
		if len(s.records) >= 1024 {
			cancel()
			return nil, errors.New("task capacity exceeded")
		}
		// No blind replay of work that may have reached an external service.
		if !terminal(d.State) {
			d.State = "interrupted"
			d.Error = "服务重启，任务已中断。请核对后重新提交。"
			d.UpdatedAt = time.Now().UTC()
			if e = s.write(d); e != nil {
				cancel()
				return nil, e
			}
		}
		if d.Notice != nil && d.Notice.State == "sending" {
			d.Notice = &Notice{State: "unknown", Error: "通知发送期间服务中断，请核对通知历史。"}
			if e = s.write(d); e != nil {
				cancel()
				return nil, e
			}
		}
		// An interrupted/canceled job's partial media is disposable; completed
		// Legacy completed artifacts remain compatible; new files use the TTL cache.
		if d.State != "succeeded" {
			os.RemoveAll(filepath.Join(root, "artifacts", id))
		}
		s.index(d)
	}
	go s.worker()
	return s, nil
}

func (s *Store) index(d diskTask) {
	meta := d.Task
	meta.Result = nil
	s.records[d.ID] = taskRecord{meta, d.Owner, d.RequestID, fingerprint(d.Name, d.Arguments)}
	s.requests[taskKey(d.Owner, d.RequestID)] = d.ID
}
func (s *Store) read(id string) (diskTask, error) {
	var d diskTask
	if !safeID.MatchString(id) {
		return d, ErrMissing
	}
	f, err := os.Open(filepath.Join(s.root, id+".json"))
	if err != nil {
		return d, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 1_000_000 {
		return d, errors.New("invalid state size")
	}
	err = json.NewDecoder(f).Decode(&d)
	return d, err
}
func (s *Store) write(d diskTask) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(raw) > 1_000_000 {
		return errors.New("task result exceeds storage limit")
	}
	temp, err := os.CreateTemp(s.root, ".task-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(raw); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, filepath.Join(s.root, d.ID+".json")); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) Submit(owner, requestID, name string, args Arguments) (Task, error) {
	return s.submit(owner, requestID, name, args, "")
}
func (s *Store) submit(owner, requestID, name string, args Arguments, upload string) (Task, error) {
	if owner == "" || !safeRequest.MatchString(requestID) {
		return Task{}, errors.New("请提供稳定、有效的请求编号。")
	}
	tool, ok := s.tools[name]
	if !ok {
		return Task{}, errors.New("不支持的后台工具。")
	}
	if len(args.Text) > MaxText*4 {
		return Task{}, errors.New("输入内容过大。")
	}
	if name != "media-clip" && args.Clip != nil {
		return Task{}, errors.New("此工具不接受截取参数。")
	}
	if err := tool.Validate(args); err != nil {
		return Task{}, err
	}
	if strings.HasPrefix(args.Text, "upload:") && (upload == "" || !tool.Uploads) {
		return Task{}, errors.New("请通过文件上传入口提交。")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil {
		return Task{}, ErrStorage
	}
	if id, ok := s.requests[taskKey(owner, requestID)]; ok {
		r := s.records[id]
		if r.fingerprint != fingerprint(name, args) {
			return Task{}, ErrConflict
		}
		return r.task, nil
	}
	if s.ctx.Err() != nil || len(s.queue) >= cap(s.queue) || len(s.records) >= 1024 {
		return Task{}, ErrBusy
	}
	id, err := newID()
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	d := diskTask{Task: Task{ID: id, Name: name, Title: tool.Title, State: "queued", CreatedAt: now, UpdatedAt: now, Href: s.base + "/tasks/" + id}, Owner: owner, RequestID: requestID, Arguments: args}
	if upload != "" {
		dir := filepath.Join(s.cacheRoot, id)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return Task{}, errors.New("临时文件存储不可用。")
		}
		if err = os.Rename(upload, filepath.Join(dir, "input")); err != nil {
			os.RemoveAll(dir)
			return Task{}, errors.New("临时文件接收失败。")
		}
	}
	if err = s.write(d); err != nil {
		if upload != "" {
			os.RemoveAll(filepath.Join(s.cacheRoot, id))
		}
		s.storageErr = err
		return Task{}, ErrStorage
	}
	s.index(d)
	s.queue <- id
	return s.withFileAvailability(d.Task), nil
}

func (s *Store) Get(owner, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil {
		return Task{}, ErrStorage
	}
	r, ok := s.records[id]
	if !ok || r.owner != owner {
		return Task{}, ErrMissing
	}
	d, err := s.read(id)
	if err != nil {
		s.storageErr = err
		return Task{}, ErrStorage
	}
	return s.withFileAvailability(d.Task), nil
}
func (s *Store) List(owner string) []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := []Task{}
	for _, r := range s.records {
		if r.owner == owner {
			tasks = append(tasks, s.withFileAvailability(r.task))
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
	if len(tasks) > 100 {
		tasks = tasks[:100]
	}
	return tasks
}
func (s *Store) Cancel(owner, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || r.owner != owner {
		return Task{}, ErrMissing
	}
	if terminal(r.task.State) {
		return r.task, nil
	}
	d, err := s.read(id)
	if err != nil {
		s.storageErr = err
		return Task{}, ErrStorage
	}
	d.State = "canceled"
	d.UpdatedAt = time.Now().UTC()
	if err = s.write(d); err != nil {
		s.storageErr = err
		return Task{}, ErrStorage
	}
	s.index(d)
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
	}
	return d.Task, nil
}

func (s *Store) worker() {
	defer close(s.done)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		s.cleanExpired(time.Now())
		ticker := time.NewTicker(s.cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-ticker.C:
				s.cleanExpired(now)
			}
		}
	}()
	defer func() { <-cleanupDone }()

	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.run(id)
		}
	}
}
func (s *Store) run(id string) {
	s.mu.Lock()
	r := s.records[id]
	if r.task.State != "queued" || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	d, err := s.read(id)
	if err != nil {
		s.storageErr = err
		s.mu.Unlock()
		return
	}
	d.State = "running"
	d.UpdatedAt = time.Now().UTC()
	if err = s.write(d); err != nil {
		s.storageErr = err
		s.mu.Unlock()
		return
	}
	s.index(d)
	tool := s.tools[d.Name]
	timeout := tool.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithCancel(s.ctx)
	if timeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(s.ctx, timeout)
	}
	s.cancels[id] = cancel
	s.mu.Unlock()
	job := Job{ID: id, URL: d.Href, Dir: filepath.Join(s.root, "artifacts", id), Report: func(stage string) error { return s.progress(id, stage) }}
	if tool.Temporary {
		job.Dir = filepath.Join(s.cacheRoot, id)
	}
	files := []Artifact{}
	job.Publish = func(file Artifact) error {
		if !artifactName.MatchString(file.Name) {
			return errors.New("invalid artifact name")
		}
		info, e := os.Lstat(filepath.Join(job.Dir, file.Name))
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("输出文件无效。")
		}
		file.Bytes = info.Size()
		files = append(files, file)
		return nil
	}
	var result Result
	var runErr error
	if tool.Artifacts {
		if tool.Temporary {
			runErr = s.prepareTemporary(job.Dir)
		} else {
			runErr = s.prepareArtifacts(job.Dir)
		}
	}
	if runErr == nil {
		if tool.Run != nil {
			result, runErr = tool.Run(ctx, d.Arguments, job)
		} else {
			result, runErr = tool.Execute(ctx, d.Arguments)
		}
	}
	cancel()
	s.mu.Lock()
	delete(s.cancels, id)
	if s.records[id].task.State == "canceled" {
		s.mu.Unlock()
		if tool.Artifacts {
			os.RemoveAll(job.Dir)
		}
		return
	}
	// Progress uses the same record; preserve its latest phase.
	d.Stage = s.records[id].task.Stage
	d.UpdatedAt = time.Now().UTC()
	if s.ctx.Err() != nil {
		d.State = "interrupted"
		d.Error = "服务停止，任务已中断。"
	} else if runErr != nil {
		d.State = "failed"
		d.Error = runErr.Error()
	} else {
		d.State = "succeeded"
		d.Result = &result
		d.Files = files
		if tool.Temporary && len(files) > 0 {
			expires := time.Now().UTC().Add(s.artifactTTL)
			d.FilesExpireAt = &expires
		}
	}
	if tool.Notify && s.notify != nil && s.ctx.Err() == nil {
		d.Notice = &Notice{State: "sending"}
	}
	if err = s.write(d); err != nil {
		d.State = "failed"
		d.Error = "任务结果保存失败，请检查存储。"
		d.Result = nil
		if s.write(d) != nil {
			s.storageErr = err
			s.mu.Unlock()
			return
		}
	}
	s.index(d)
	s.mu.Unlock()
	if tool.Artifacts && d.State != "succeeded" {
		os.RemoveAll(job.Dir)
	}
	// Result is durable before a completion notification can link to it.
	if d.Notice != nil && d.Notice.State == "sending" {
		ctx, stop := context.WithTimeout(s.ctx, 15*time.Second)
		messageID, noticeErr := s.notify(ctx, d.Task)
		stop()
		s.mu.Lock()
		d.Notice = &Notice{State: "sent", ID: messageID}
		if noticeErr != nil {
			d.Notice = &Notice{State: "failed", Error: "通知未确认送达，任务结果已保留。"}
		}
		d.UpdatedAt = time.Now().UTC()
		if err := s.write(d); err != nil {
			s.storageErr = err
		} else {
			s.index(d)
		}
		s.mu.Unlock()
	}
}

func (s *Store) progress(id, stage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[id].task.State != "running" {
		return context.Canceled
	}
	d, err := s.read(id)
	if err != nil {
		s.storageErr = err
		return ErrStorage
	}
	if len(stage) > 256 {
		return errors.New("invalid phase")
	}
	if d.Stage == stage {
		return nil
	}
	d.Stage, d.UpdatedAt = stage, time.Now().UTC()
	if err = s.write(d); err != nil {
		s.storageErr = err
		return ErrStorage
	}
	s.index(d)
	return nil
}
func (s *Store) Close()         { s.cancel(); <-s.done }
func (s *Store) Healthy() error { s.mu.Lock(); defer s.mu.Unlock(); return s.storageErr }
