package toolkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDouyinShareValidation(t *testing.T) {
	for _, s := range []string{"分享文案 https://v.douyin.com/cb1uaat3CPM/ 复制打开抖音", "https://www.douyin.com/video/7692776036492273326", "https://www.douyin.com/jingxuan?modal_id=7692776036492273326"} {
		if _, e := douyinURL(s); e != nil {
			t.Fatalf("%s: %v", s, e)
		}
		if a := Suggest(s, "text"); len(a) == 0 || a[0].HandlerID != "douyin-download" {
			t.Fatalf("missing suggestion: %+v", a)
		}
	}
	for _, s := range []string{"https://douyin.com.evil.test/video/7692776036492273326", "https://www.douyin.com@127.0.0.1/video/7692776036492273326", "https://www.douyin.com:8080/video/7692776036492273326", "https://www.douyin.com/user/123", "https://www.douyin.com/note/7692776036492273326", "https://v.douyin.com/a/ https://v.douyin.com/b/", "https://127.0.0.1/"} {
		if _, e := douyinURL(s); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	for _, s := range []string{"https://127.0.0.1/a", "https://aweme.snssdk.com.evil.test/a", "https://example.org/video.mp4"} {
		if _, e := douyinMediaURL(s); e == nil {
			t.Fatalf("accepted media %s", s)
		}
	}
	u, e := douyinMediaURL("https://aweme.snssdk.com/aweme/v1/play/?video_id=x&watermark=1&logo_name=x&ratio=720p")
	if e != nil || u.Query().Has("watermark") || u.Query().Has("logo_name") || u.Query().Get("ratio") != "720p" {
		t.Fatal(u, e)
	}
}

type douyinTransport func(*http.Request) (*http.Response, error)

func (f douyinTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDouyinGuestSessionAndRedirectBoundary(t *testing.T) {
	client := newDouyinClient()
	requests := 0
	client.Transport = douyinTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := "home"
		headers := http.Header{"Set-Cookie": []string{"ttwid=test-guest; Domain=iesdouyin.com; Path=/; Secure"}}
		if r.URL.Path != "/" {
			cookie, e := r.Cookie("ttwid")
			if e != nil || cookie.Value != "test-guest" {
				t.Fatal("guest session not retained")
			}
			body = `<script>window._ROUTER_DATA={"loaderData":{"video_(id)/page":{"videoInfoRes":{"item_list":[{"aweme_id":"7692776036492273326","video":{"play_addr":{"url_list":["https://aweme.snssdk.com/aweme/v1/playwm/?video_id=x"]}}}]}}}}</script>`
		}
		return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	if _, e := resolveDouyin(context.Background(), client, "https://www.douyin.com/video/7692776036492273326"); e != nil {
		t.Fatal(e)
	}
	if requests != 2 {
		t.Fatal(requests)
	}
	other := newDouyinClient()
	u, _ := http.NewRequest("GET", "https://www.iesdouyin.com/", nil)
	if len(other.Jar.Cookies(u.URL)) != 0 {
		t.Fatal("sessions leaked across jobs")
	}
	bad, _ := http.NewRequest("GET", "https://evil.test/", nil)
	if other.CheckRedirect(bad, []*http.Request{u}) == nil {
		t.Fatal("off-platform redirect accepted")
	}
}

// Explicit opt-in only. Uses a public link independently found in yt-dlp #9667;
// no account cookies, browser process, third-party API or notifications.
func TestDouyinLive(t *testing.T) {
	if os.Getenv("HAVEN_DOUYIN_LIVE") != "1" {
		t.Skip("manual public-network acceptance")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var files []Artifact
	r, e := runDouyin(ctx, Arguments{Text: "分享 https://v.douyin.com/cb1uaat3CPM/"}, Job{Dir: root, URL: "/tools/tasks/test", Report: func(s string) error { t.Log(s); return nil }, Publish: func(a Artifact) error { files = append(files, a); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(root, "video.mp4"))
	if e != nil || info.Size() == 0 || len(files) != 1 {
		t.Fatal("artifact missing", e)
	}
	fmt.Printf("LIVE Douyin title=%q bytes=%d fields=%+v\n", r.Title, info.Size(), r.Fields)
}
