package toolkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/HaltCatch/haven-tools/internal/douyinparse"
	"golang.org/x/net/publicsuffix"
)

const douyinMobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) EdgiOS/121.0.2277.107 Version/17.0 Mobile/15E148 Safari/604.1"

var shareURLPattern = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)
var douyinVideoPath = regexp.MustCompile(`^/(?:share/)?video/([0-9]{15,25})/?$`)
var douyinIDPattern = regexp.MustCompile(`^[0-9]{15,25}$`)
var douyinShortPath = regexp.MustCompile(`^/[A-Za-z0-9_-]+/?$`)

func douyinHost(host string) bool {
	switch strings.ToLower(host) {
	case "douyin.com", "www.douyin.com", "www.iesdouyin.com", "v.douyin.com":
		return true
	}
	return false
}
func douyinURL(text string) (*url.URL, error) {
	links := shareURLPattern.FindAllString(text, -1)
	if len(links) != 1 {
		return nil, errors.New("请粘贴一条抖音视频分享链接，整段分享文案也可以。")
	}
	u, err := resourceURL(strings.TrimRight(links[0], ".,;!?)"))
	if err != nil || !douyinHost(u.Hostname()) {
		return nil, errors.New("请使用抖音官方分享链接。")
	}
	if u.Hostname() == "v.douyin.com" {
		if !douyinShortPath.MatchString(u.Path) {
			return nil, errors.New("抖音分享短链格式无效。")
		}
	} else if douyinVideoID(u) == "" {
		return nil, errors.New("仅支持单个抖音视频，不支持主页、直播或图集。")
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Hostname())
	u.Fragment = ""
	return u, nil
}
func douyinVideoID(u *url.URL) string {
	if !douyinHost(u.Hostname()) {
		return ""
	}
	if m := douyinVideoPath.FindStringSubmatch(u.Path); len(m) == 2 {
		return m[1]
	}
	if u.Path == "/jingxuan" || u.Path == "/discover" || u.Path == "/" {
		if id := u.Query().Get("modal_id"); douyinIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}
func newDouyinClient() *http.Client {
	client := resourceClient()
	client.Jar, _ = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	check := client.CheckRedirect
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if !douyinHost(r.URL.Hostname()) {
			return errors.New("分享链接跳转到不支持的站点。")
		}
		return check(r, via)
	}
	return client
}
func douyinPage(ctx context.Context, client *http.Client, raw string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", douyinMobileUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", "https://www.douyin.com/")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, errors.New("无法读取抖音分享页：网络、跳转或平台限制。")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("抖音分享页返回 HTTP %d。", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return nil, nil, errors.New("抖音分享页读取中断或过大。")
	}
	return b, resp.Request.URL, nil
}
func resolveDouyin(ctx context.Context, client *http.Client, text string) (douyinparse.Video, error) {
	u, err := douyinURL(text)
	if err != nil {
		return douyinparse.Video{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// A fresh in-memory guest session, never the user's browser/login cookies.
	// The share page omits item_list on the first request without ttwid.
	if _, _, err = douyinPage(ctx, client, "https://www.iesdouyin.com/"); err != nil {
		return douyinparse.Video{}, err
	}
	if u.Hostname() == "v.douyin.com" {
		_, u, err = douyinPage(ctx, client, u.String())
		if err != nil {
			return douyinparse.Video{}, err
		}
	}
	id := douyinVideoID(u)
	if id == "" {
		return douyinparse.Video{}, errors.New("短链没有指向支持的单个视频。")
	}
	var video douyinparse.Video
	for attempt := 0; attempt < 2; attempt++ {
		b, _, e := douyinPage(ctx, client, "https://www.iesdouyin.com/share/video/"+id+"/")
		if e != nil {
			return video, e
		}
		video, err = douyinparse.Parse(b, id)
		if err == nil {
			return video, nil
		}
	}
	return video, err
}
func douyinMediaHost(host string) bool {
	host = strings.ToLower(host)
	if host == "aweme.snssdk.com" || host == "www.iesdouyin.com" || host == "www.douyin.com" {
		return true
	}
	for _, domain := range []string{"douyinvod.com", "ixigua.com", "bytecdn.cn", "bytecdn.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
func douyinMediaURL(raw string) (*url.URL, error) {
	u, err := resourceURL(raw)
	if err != nil || !douyinMediaHost(u.Hostname()) {
		return nil, errors.New("平台返回了不支持的视频地址。")
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Hostname())
	if u.Path == "/aweme/v1/play/" {
		q := u.Query()
		q.Del("watermark")
		q.Del("logo_name")
		u.RawQuery = q.Encode()
	}
	return u, nil
}
func douyinTask() AsyncTool {
	t, _ := findTool("douyin-download")
	return AsyncTool{Title: t.Title, Timeout: -1, Artifacts: true, Temporary: true,
		Validate: func(a Arguments) error { _, err := douyinURL(a.Text); return err },
		Run:      runDouyin,
	}
}
func runDouyin(ctx context.Context, a Arguments, j Job) (Result, error) {
	if err := j.Report("解析抖音分享链接"); err != nil {
		return Result{}, err
	}
	client := newDouyinClient()
	defer client.CloseIdleConnections()
	video, err := resolveDouyin(ctx, client, a.Text)
	if err != nil {
		return Result{}, err
	}
	mediaURL, err := douyinMediaURL(video.URL)
	if err != nil {
		return Result{}, err
	}
	if err = j.Report("下载平台无水印视频源"); err != nil {
		return Result{}, err
	}
	// Reuse Haven's public-IP dial validation, disk-space guard and streaming
	// writer; do not send the visitor jar to a CDN or expose signed URLs.
	downloadClient := resourceClient()
	defer downloadClient.CloseIdleConnections()
	check := downloadClient.CheckRedirect
	downloadClient.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if !douyinMediaHost(r.URL.Hostname()) {
			return errors.New("视频跳转到不支持的站点。")
		}
		return check(r, via)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", douyinMobileUA)
	dest := filepath.Join(j.Dir, "video.mp4")
	meta, err := fetchResourceRequest(downloadClient, req, dest, 0)
	if err != nil {
		return Result{}, err
	}
	if err = j.Report("检查视频文件"); err != nil {
		return Result{}, err
	}
	duration, err := mediaDuration(ctx, dest)
	if err != nil {
		return Result{}, errors.New("下载内容不是有效的视频文件。")
	}
	// An audio-only or HTML response must not become a successful video result.
	probe := &limitedBuffer{limit: 4096}
	if err = runMediaCommand(ctx, dest, "ffprobe", []string{"-v", "error"}, []string{"-select_streams", "v:0", "-show_entries", "stream=codec_type", "-of", "default=noprint_wrappers=1:nokey=1"}, probe); err != nil || strings.TrimSpace(probe.String()) != "video" {
		return Result{}, errors.New("下载文件缺少视频轨道。")
	}
	if err = publish(j, "video.mp4", "下载无水印视频", "douyin-"+video.ID+".mp4", "video/mp4"); err != nil {
		return Result{}, err
	}
	result := resultFor("douyin-download", "视频已准备好，点击下载即可保存。")
	if video.Title != "" {
		result.Title = video.Title
	}
	result.Description = "获取平台提供的无水印视频源；作者嵌入画面的文字或标识会保留。文件保留 24 小时。"
	result.Fields = []Field{{"作者", video.Author}, {"时长", fmt.Sprintf("%.2f 秒", duration.Seconds())}, {"大小", fmt.Sprintf("%.2f MiB", float64(meta.Bytes)/(1<<20))}}
	result.Links = []Link{{"下载无水印视频", j.URL + "/files/video.mp4"}, {"打开原作品", "https://www.douyin.com/video/" + video.ID}}
	return result, nil
}
