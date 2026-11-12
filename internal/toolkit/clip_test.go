package toolkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClipModesRejectInvalidAndProduceBoundedMedia(t *testing.T) {
	for _, o := range []*ClipOptions{nil, {Start: -1, End: 2, Mode: "copy"}, {Start: 2, End: 1, Mode: "exact"}, {Start: 0, End: 2, Mode: "shell"}} {
		if validateClip(o) == nil {
			t.Fatal("accepted invalid clip", o)
		}
	}
	for _, mode := range []string{"copy", "exact", "audio"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input")
			nativeMedia(t, "-f", "lavfi", "-i", "testsrc2=size=128x128:rate=25", "-f", "lavfi", "-i", "sine=frequency=440", "-t", "3", "-c:v", "libx264", "-g", "25", "-c:a", "aac", "-f", "matroska", input)
			published := ""
			job := Job{Dir: dir, URL: "/task", Report: func(string) error { return nil }, Publish: func(a Artifact) error { published = a.Name; return nil }}
			result, err := clipMedia(context.Background(), Arguments{Clip: &ClipOptions{Start: 1, End: 2, Mode: mode}}, job, input)
			if err != nil || published == "" || len(result.Links) != 1 {
				t.Fatal(result, err)
			}
			duration, err := mediaDuration(context.Background(), filepath.Join(dir, published))
			if err != nil || duration < 900*time.Millisecond || duration > 1500*time.Millisecond && mode != "copy" || duration > 3*time.Second {
				t.Fatal(duration, err)
			}
			if _, err = os.Stat(input); !os.IsNotExist(err) {
				t.Fatal("input retained")
			}
		})
	}
}
func TestClipOptionsParticipateInRequestIdentity(t *testing.T) {
	a := Arguments{Text: "upload:" + strings.Repeat("a", 64), Clip: &ClipOptions{Start: 1, End: 2, Mode: "copy"}}
	b := a
	b.Clip = &ClipOptions{Start: 2, End: 3, Mode: "copy"}
	if fingerprint("media-clip", a) == fingerprint("media-clip", b) {
		t.Fatal("options ignored")
	}
	if _, err := decodeArguments([]byte(`{"text":"x","clip":{"start":0,"end":1,"mode":"copy","script":"x"}}`)); err == nil {
		t.Fatal("unknown option accepted")
	}
}
