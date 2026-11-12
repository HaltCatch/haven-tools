package toolkit

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var errDiskSpace = errors.New("临时磁盘空间不足，请清理文件或扩容后重试。")

// This is free filesystem space, not a per-file or cache quota.
func checkDiskSpace(path string, incoming int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if stat.Bavail <= 0 || stat.Bsize <= 0 {
		return errDiskSpace
	}
	reserve := max(uint64(64<<20), uint64(stat.Blocks)*uint64(stat.Bsize)*15/100)
	if uint64(stat.Bavail)*uint64(stat.Bsize) < uint64(incoming)+reserve {
		return errDiskSpace
	}
	return nil
}

type diskWriter struct{ file *os.File }

func (w diskWriter) Write(p []byte) (int, error) {
	if err := checkDiskSpace(w.file.Name(), int64(len(p))); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func copyToDisk(file *os.File, source io.Reader) (int64, error) {
	return io.Copy(diskWriter{file}, source)
}

// Native tools write their own files. Stop the whole process group if the
// filesystem runs low, while retaining ordinary cancellation and no size cap.
func runWithDiskSpace(ctx context.Context, dir string, run func(context.Context) error) error {
	if err := checkDiskSpace(dir, 0); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := checkDiskSpace(dir, 0); err != nil {
				cancel()
				<-done
				return err
			}
		}
	}
}
