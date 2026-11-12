package toolkit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Artifact struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	DownloadName string `json:"downloadName"`
	ContentType  string `json:"contentType"`
	Bytes        int64  `json:"bytes"`
}

var artifactName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

func (s *Store) withFileAvailability(t Task) Task {
	if t.FilesExpireAt == nil {
		return t
	}
	available := time.Now().Before(*t.FilesExpireAt) && len(t.Files) > 0
	for _, file := range t.Files {
		if !artifactName.MatchString(file.Name) {
			available = false
			break
		}
		info, e := os.Lstat(filepath.Join(s.cacheRoot, t.ID, file.Name))
		if e != nil || !info.Mode().IsRegular() {
			available = false
			break
		}
	}
	t.FilesAvailable = &available
	return t
}
func (s *Store) cleanExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := os.ReadDir(s.cacheRoot)
	for _, entry := range entries {
		path := filepath.Join(s.cacheRoot, entry.Name())
		if safeID.MatchString(entry.Name()) {
			record, exists := s.records[entry.Name()]
			if exists && !terminal(record.task.State) {
				continue
			}
			if exists && record.task.FilesExpireAt != nil && now.Before(*record.task.FilesExpireAt) && record.task.State == "succeeded" {
				continue
			}
			os.RemoveAll(path)
		} else if !entry.IsDir() {
			info, e := entry.Info()
			if e == nil && now.Sub(info.ModTime()) > time.Hour {
				os.Remove(path)
			}
		}
	}
}
func (s *Store) prepareTemporary(dir string) error {
	if err := os.MkdirAll(s.cacheRoot, 0700); err != nil {
		return err
	}
	s.cleanExpired(time.Now())
	if err := checkDiskSpace(s.cacheRoot, 0); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0700)
}
func (s *Service) upload(w http.ResponseWriter, r *http.Request) {
	tool, ok := s.store.tools[r.PathValue("name")]
	if !ok || !tool.Uploads {
		s.send(w, 400, map[string]any{"error": "该工具不支持文件上传。"})
		return
	}
	if !safeRequest.MatchString(r.Header.Get("X-Request-ID")) {
		s.send(w, 400, map[string]any{"error": "请提供稳定的请求编号。"})
		return
	}
	args := Arguments{}
	if value := r.Header.Get("X-Tool-Options"); value != "" {
		var clip ClipOptions
		if strictJSON([]byte(value), &clip) != nil {
			s.send(w, 400, map[string]any{"error": "截取参数无效。"})
			return
		}
		args.Clip = &clip
	}
	if r.PathValue("name") == "media-clip" {
		if err := validateClip(args.Clip); err != nil {
			s.send(w, 400, map[string]any{"error": err.Error()})
			return
		}
	} else if args.Clip != nil {
		s.send(w, 400, map[string]any{"error": "此工具不接受截取参数。"})
		return
	}
	if err := os.MkdirAll(s.store.cacheRoot, 0700); err != nil {
		s.taskError(w, ErrStorage)
		return
	}
	f, err := os.CreateTemp(s.store.cacheRoot, ".upload-*")
	if err != nil {
		s.taskError(w, ErrStorage)
		return
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	controller := http.NewResponseController(w)
	// Uploads use an idle deadline; a continuously progressing large upload is
	// not constrained by the server's ordinary 15/30 second request deadlines.
	controller.SetWriteDeadline(time.Time{})
	defer r.Body.Close()
	n, err := io.Copy(io.MultiWriter(diskWriter{f}, hash), uploadReader{r.Body, controller})
	closeErr := f.Close()
	if errors.Is(err, errDiskSpace) {
		s.send(w, 507, map[string]any{"error": errDiskSpace.Error()})
		return
	}
	if err != nil || closeErr != nil || n == 0 {
		s.send(w, 400, map[string]any{"error": "文件为空、上传中断或临时存储不可用。"})
		return
	}
	args.Text = "upload:" + hex.EncodeToString(hash.Sum(nil))
	task, err := s.store.submit(owner(r.Context()), r.Header.Get("X-Request-ID"), r.PathValue("name"), args, f.Name())
	if err != nil {
		s.taskError(w, err)
		return
	}
	s.send(w, 202, map[string]any{"task": task})
}

type uploadReader struct {
	io.Reader
	controller *http.ResponseController
}

func (r uploadReader) Read(p []byte) (int, error) {
	r.controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	return r.Reader.Read(p)
}
