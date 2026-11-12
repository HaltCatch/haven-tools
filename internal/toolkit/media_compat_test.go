package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func nativeMedia(t *testing.T, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("native FFmpeg required")
	}
	cmd := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("FFmpeg fixture: %s: %v", output, err)
	}
}

func TestNativeMediaProbesContentAndRejectsExternalResources(t *testing.T) {
	for _, format := range []string{"aiff", "asf", "ac3", "ogg", "flac", "mp4"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "wrong-extension.txt")
			codec := "aac"
			switch format {
			case "aiff":
				codec = "pcm_s16be"
			case "asf":
				codec = "wmav2"
			case "ac3":
				codec = "ac3"
			case "ogg":
				codec = "libopus"
			case "flac":
				codec = "flac"
			}
			nativeMedia(t, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", codec, "-f", format, input)
			if err := checkMedia(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			job := Job{Dir: dir, Report: func(string) error { return nil }}
			if err := os.Rename(input, filepath.Join(dir, "input")); err != nil {
				t.Fatal(err)
			}
			if _, err := runResource(context.Background(), "media-audio", Arguments{Text: "upload:" + strings.Repeat("a", 64)}, job, MediaConfig{}); err != nil {
				t.Fatal(err)
			}
			if err := checkMedia(context.Background(), filepath.Join(dir, "audio.m4a")); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A playlist pointing at a valid local audio file must still be rejected.
	dir := t.TempDir()
	secret := filepath.Join(dir, "private.wav")
	nativeMedia(t, "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.1", secret)
	for _, body := range []string{"ffconcat version 1.0\nfile '" + secret + "'\n", "#EXTM3U\n#EXTINF:1,\nhttps://example.com/audio.wav\n"} {
		input := filepath.Join(dir, "playlist")
		os.WriteFile(input, []byte(body), 0600)
		if err := checkMedia(context.Background(), input); err == nil {
			t.Fatal("external resource accepted")
		}
	}
}

func TestLongMediaSegmentsWithoutDurationRejectionOrTruncation(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	nativeMedia(t, "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "1861", "-c:a", "flac", "-f", "flac", input)
	calls, total := 0, 0.0
	var large atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		part, err := reader.NextPart()
		if err != nil || !strings.HasSuffix(part.FileName(), ".wav") {
			t.Error("expected normalized WAV")
			w.WriteHeader(400)
			return
		}
		file, err := os.CreateTemp(dir, ".received-*.wav")
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer os.Remove(file.Name())
		_, err = io.Copy(file, part)
		file.Close()
		if err != nil {
			t.Error(err)
		}
		// Native FFprobe measures what actually reached Whisper, including tail.
		probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", file.Name()).Output()
		var duration float64
		fmt.Sscan(string(probe), &duration)
		if err != nil || duration <= 0 || duration > 600.2 {
			t.Errorf("unbounded/invalid segment: %s %v", probe, err)
		}
		total += duration
		calls++
		// Silence in a middle segment must not discard later speech.
		if large.Load() {
			json.NewEncoder(w).Encode(speechResponse{Text: strings.Repeat("你", 100001), Segments: []speechSegment{{Start: 0, End: 1, Text: "subtitle"}}})
		} else if calls == 2 {
			io.WriteString(w, `{"text":""}`)
		} else {
			json.NewEncoder(w).Encode(speechResponse{Text: fmt.Sprintf("segment %d", calls), Segments: []speechSegment{{Start: 0, End: 1, Text: fmt.Sprintf("segment %d", calls)}}})
		}
	}))
	defer server.Close()
	job := Job{Dir: dir, Report: func(string) error { return nil }}
	text, err := transcribeMedia(context.Background(), MediaConfig{WhisperURL: server.URL}, input, job)
	if err != nil || calls != 4 || total < 1860.9 || total > 1861.1 || text != "segment 1\nsegment 3\nsegment 4" {
		t.Fatal(text, calls, total, err)
	}
	large.Store(true)
	text, err = transcribeMedia(context.Background(), MediaConfig{WhisperURL: server.URL}, input, job)
	full, readErr := os.ReadFile(filepath.Join(dir, "transcript.txt"))
	if err != nil || readErr != nil || len(full) != 4*300003+3 || len(text) > 601000 || !utf8.ValidString(text) || !strings.Contains(text, "完整文字请下载") {
		t.Fatal("long transcript lost", len(full), len(text), err, readErr)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, ".speech-*")); len(files) != 0 {
		t.Fatal("segments leaked")
	}
}

type zeroSource struct{}

func (zeroSource) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUploadAndArtifactsExceedOldByteCaps(t *testing.T) {
	const size = 129 << 20
	tool := AsyncTool{Title: "stream fixture", Uploads: true, Temporary: true, Artifacts: true, Timeout: -1,
		Validate: func(Arguments) error { return nil }, Run: func(ctx context.Context, a Arguments, job Job) (Result, error) {
			if err := os.Rename(filepath.Join(job.Dir, "input"), filepath.Join(job.Dir, "download.bin")); err != nil {
				return Result{}, err
			}
			if err := publish(job, "download.bin", "download", "input.dat", "application/octet-stream"); err != nil {
				return Result{}, err
			}
			return Result{Type: "text", Title: "complete", Text: "complete"}, nil
		}}
	store, err := OpenStore(t.TempDir(), "/tools", map[string]AsyncTool{"media-transcribe": tool}, StoreOptions{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	request := httptest.NewRequest("POST", "http://tools/v1/files/media-transcribe", io.LimitReader(zeroSource{}, size))
	request.Header.Set("X-Request-ID", "large-stream")
	request.SetPathValue("name", "media-transcribe")
	request = request.WithContext(context.WithValue(request.Context(), identityKey{}, "alice"))
	w := httptest.NewRecorder()
	(&Service{store: store}).upload(w, request)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Task Task `json:"task"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	waitTask(t, store, "alice", response.Task.ID, "succeeded")
	task, err := store.Get("alice", response.Task.ID)
	if err != nil || len(task.Files) != 1 || task.Files[0].Bytes != size {
		t.Fatal(task, err)
	}
	// The same streaming downloader accepts both known and chunked sizes.
	old := fetchClient
	defer func() { fetchClient = old }()
	for _, length := range []int64{size, -1} {
		fetchClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: length, Header: http.Header{}, Body: io.NopCloser(io.LimitReader(zeroSource{}, size)), Request: r}, nil
		})}
		file := filepath.Join(t.TempDir(), "download")
		meta, err := fetchResource(context.Background(), "https://example.com/opaque", file, 0)
		if err != nil || meta.Bytes != size {
			t.Fatal(meta, err)
		}
	}
}

func TestDiskGuardCancellationReapsNativeProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := runWithDiskSpace(ctx, t.TempDir(), func(ctx context.Context) error { return runBounded(exec.CommandContext(ctx, "sleep", "30")) })
	if err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestQRStreamBeyondOldUploadLimit(t *testing.T) {
	service, _ := testService(t)
	input := io.MultiReader(bytes.NewReader(qrImage(t, "large-upload-test", false, false)), io.LimitReader(zeroSource{}, 5<<20))
	r := httptest.NewRequest("POST", "http://tools/v1/image", input)
	w := httptest.NewRecorder()
	service.image(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "large-upload-test") {
		t.Fatal(w.Code, w.Body.String())
	}
}
