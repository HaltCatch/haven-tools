package toolkit

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) prepareArtifacts(dir string) error {
	if err := checkDiskSpace(s.root, 0); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0700)
}

func syncArtifacts(dir string) error {
	for _, name := range []string{"video.mp4", "transcript.txt"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, path := range []string{dir, filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))} {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) artifact(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.Get(owner(r.Context()), r.PathValue("id"))
	if err != nil {
		s.taskError(w, err)
		return
	}
	name := r.PathValue("file")
	if task.State != "succeeded" || !artifactName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(s.store.root, "artifacts", task.ID)
	descriptor := Artifact{Name: name, DownloadName: name}
	if task.FilesExpireAt != nil {
		if task.FilesAvailable == nil || !*task.FilesAvailable {
			http.Error(w, "临时文件已清理，任务中的文字结果仍保留。", http.StatusGone)
			return
		}
		root = filepath.Join(s.store.cacheRoot, task.ID)
		found := false
		for _, file := range task.Files {
			if file.Name == name {
				descriptor = file
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	} else if task.Name != "bilibili-transcribe" || (name != "video.mp4" && name != "transcript.txt") {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": descriptor.DownloadName}))
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if descriptor.ContentType != "" {
		w.Header().Set("Content-Type", descriptor.ContentType)
	} else if name == "video.mp4" {
		w.Header().Set("Content-Type", "video/mp4")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}
