package toolkit

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranscriptionStreamsMultipartAndAllowsSilentSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")
	os.WriteFile(path, []byte("video-data"), 0600)
	for _, body := range []string{`{"text":"你好，世界\n第二行"}`, `{"text":"  "}`, strings.Repeat("x", 800001)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
			}
			defer r.MultipartForm.RemoveAll()
			f, h, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			raw, _ := io.ReadAll(f)
			if string(raw) != "video-data" || h.Filename != "video.mp4" || r.FormValue("response_format") != "json" || r.FormValue("language") != "auto" {
				t.Error("invalid Whisper multipart")
			}
			w.Write([]byte(body))
		}))
		text, err := transcribeFile(context.Background(), server.URL, path, nil)
		if strings.HasPrefix(body, `{"text":"你好`) {
			if err != nil || text != "你好，世界\n第二行" {
				t.Fatal(text, err)
			}
		} else if body == `{"text":"  "}` {
			if err != nil || text != "" {
				t.Fatal(text, err)
			}
		} else if err == nil {
			t.Fatal("invalid transcript accepted")
		}
		server.Close()
	}
}

func TestMediaArtifactsProgressOwnershipRestartAndNoticeFailure(t *testing.T) {
	root := t.TempDir()
	noticeCalls := atomic.Int32{}
	tool := AsyncTool{Title: "media fixture", Artifacts: true, Notify: true, Validate: func(Arguments) error { return nil }, Run: func(ctx context.Context, a Arguments, job Job) (Result, error) {
		if err := job.Report("GPU 转写"); err != nil {
			return Result{}, err
		}
		os.WriteFile(filepath.Join(job.Dir, "video.mp4"), []byte("video-data"), 0600)
		os.WriteFile(filepath.Join(job.Dir, "transcript.txt"), []byte("中文结果"), 0600)
		if err := syncArtifacts(job.Dir); err != nil {
			return Result{}, err
		}
		return Result{Type: "text", Title: "media result", Text: "中文结果"}, nil
	}}
	store, err := OpenStore(root, "/tools", map[string]AsyncTool{"bilibili-transcribe": tool})
	if err != nil {
		t.Fatal(err)
	}
	store.notify = func(ctx context.Context, task Task) (string, error) {
		noticeCalls.Add(1)
		saved, e := store.Get("owner", task.ID)
		if e != nil || saved.Result == nil || saved.Notice.State != "sending" {
			t.Error("notification preceded durable result", e)
		}
		return "", errors.New("simulated notify outage")
	}
	task, err := store.Submit("owner", "media-request", "bilibili-transcribe", Arguments{Text: "input"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, store, "owner", task.ID, "succeeded")
	store.Close() // waits for the notification state to become durable too.
	final, err := store.Get("owner", task.ID)
	if err != nil || final.Notice.State != "failed" || final.Result.Text != "中文结果" || final.Stage != "GPU 转写" || noticeCalls.Load() != 1 {
		t.Fatal(final, err)
	}
	service := &Service{store: store}
	for _, check := range []struct {
		who, name string
		status    int
	}{{"owner", "video.mp4", 200}, {"owner", "transcript.txt", 200}, {"other", "video.mp4", 404}, {"owner", "../x", 404}, {"owner", "state.json", 404}} {
		request := httptest.NewRequest("GET", "http://tools.test/", nil)
		request.SetPathValue("id", task.ID)
		request.SetPathValue("file", check.name)
		request = request.WithContext(context.WithValue(request.Context(), identityKey{}, check.who))
		response := httptest.NewRecorder()
		service.artifact(response, request)
		if response.Code != check.status {
			t.Fatal(check, response.Code)
		}
	}
	restored, err := OpenStore(root, "/tools", map[string]AsyncTool{"bilibili-transcribe": tool})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	read, err := restored.Get("owner", task.ID)
	if err != nil || read.Result.Text != "中文结果" || read.Notice.State != "failed" {
		t.Fatal("restore lost media result", err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "artifacts", task.ID, "video.mp4")); err != nil || string(raw) != "video-data" {
		t.Fatal("restore lost media", err)
	}
}

func TestCanceledMediaRemovesPartialDownloadAndDoesNotNotify(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	tool := AsyncTool{Title: "media", Artifacts: true, Notify: true, Validate: func(Arguments) error { return nil }, Run: func(ctx context.Context, _ Arguments, job Job) (Result, error) {
		os.WriteFile(filepath.Join(job.Dir, "video.part"), []byte("partial"), 0600)
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	store, err := OpenStore(root, "/tools", map[string]AsyncTool{"media": tool})
	if err != nil {
		t.Fatal(err)
	}
	store.notify = func(context.Context, Task) (string, error) {
		t.Error("canceled task sent a completion notice")
		return "", nil
	}
	task, err := store.Submit("owner", "cancel", "media", Arguments{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	store.Cancel("owner", task.ID)
	store.Close()
	if _, err := os.Stat(filepath.Join(root, "artifacts", task.ID)); !os.IsNotExist(err) {
		t.Fatal("partial media retained", err)
	}
}

func TestNotificationTLSAuthenticationAndStableResultLink(t *testing.T) {
	var message map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "test-user" || p != "test-password" {
			t.Error("notification authentication missing")
		}
		json.NewDecoder(r.Body).Decode(&message)
		io.WriteString(w, `{"id":"message-id"}`)
	}))
	defer server.Close()
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	send, err := notifier(NotificationConfig{URL: server.URL, Username: "test-user", Password: "test-password", CAFile: certPath})
	if err != nil {
		t.Fatal(err)
	}
	id, err := send(context.Background(), Task{ID: "fixture", State: "succeeded", Href: "https://homepage.example/tools/tasks/fixture", Result: &Result{Title: "视频"}})
	if err != nil || id != "message-id" || message["click"] != "https://homepage.example/tools/tasks/fixture" || message["topic"] != "cyberdeck-tasks" {
		t.Fatal(message, id, err)
	}
	// System trust alone must reject the isolated test issuer.
	untrusted, _ := notifier(NotificationConfig{URL: server.URL, Username: "test-user", Password: "test-password"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := untrusted(ctx, Task{}); err == nil {
		t.Fatal("untrusted notification TLS accepted")
	}
}
