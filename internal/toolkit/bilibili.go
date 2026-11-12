package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var videoPath = regexp.MustCompile(`^/video/(BV[0-9A-Za-z]{10}|av[0-9]{1,16})(?:/|$)`)
var shortPath = regexp.MustCompile(`^/[0-9A-Za-z]{3,24}/?$`)

func bilibiliID(text string) (string, error) {
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || len(text) > 2048 {
		return "", errors.New("请输入有效的 B 站 HTTPS 视频链接。")
	}
	host := strings.ToLower(u.Hostname())
	if host == "b23.tv" && shortPath.MatchString(u.Path) {
		return "short", nil
	}
	if host != "www.bilibili.com" && host != "bilibili.com" && host != "m.bilibili.com" {
		return "", errors.New("不是支持的 B 站视频链接。")
	}
	match := videoPath.FindStringSubmatch(u.Path)
	if match == nil {
		return "", errors.New("链接中没有有效的视频编号。")
	}
	return match[1], nil
}

func ReadBilibili(ctx context.Context, text string, transport http.RoundTripper) (Result, error) {
	id, err := bilibiliID(text)
	if err != nil {
		return Result{}, err
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	if id == "short" {
		req, _ := http.NewRequestWithContext(ctx, "GET", text, nil)
		req.Header.Set("User-Agent", "HavenTools/1.0")
		response, e := client.Do(req)
		if e != nil {
			return Result{}, errors.New("短链接读取失败。")
		}
		response.Body.Close()
		if response.StatusCode < 300 || response.StatusCode > 399 {
			return Result{}, errors.New("短链接未返回视频地址。")
		}
		next, e := response.Location()
		if e != nil {
			return Result{}, errors.New("短链接地址无效。")
		}
		id, err = bilibiliID(next.String())
		if err != nil || id == "short" {
			return Result{}, errors.New("短链接未指向支持的视频地址。")
		}
	}
	endpoint := "https://api.bilibili.com/x/web-interface/view?bvid=" + url.QueryEscape(id)
	if strings.HasPrefix(id, "av") {
		endpoint = "https://api.bilibili.com/x/web-interface/view?aid=" + id[2:]
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 HavenTools/1.0")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	response, err := client.Do(req)
	if err != nil {
		return Result{}, errors.New("视频信息读取失败，请稍后重试。")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Result{}, fmt.Errorf("B 站返回 HTTP %d，未取得结果。", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1_000_001))
	if err != nil || len(data) > 1_000_000 {
		return Result{}, errors.New("视频信息响应无效或过大。")
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			BVID     string `json:"bvid"`
			Title    string `json:"title"`
			Desc     string `json:"desc"`
			Duration int    `json:"duration"`
			Pubdate  int64  `json:"pubdate"`
			Owner    struct {
				Name string `json:"name"`
			} `json:"owner"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Code != 0 || payload.Data.Title == "" || !regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`).MatchString(payload.Data.BVID) {
		return Result{}, errors.New("视频不存在、不可见或接口暂不可用。")
	}
	p := payload.Data
	fields := []Field{{"作者", p.Owner.Name}, {"时长", fmt.Sprintf("%d:%02d", p.Duration/60, p.Duration%60)}, {"发布", time.Unix(p.Pubdate, 0).In(shanghai).Format("2006-01-02 15:04")}, {"视频编号", p.BVID}}
	if len(p.Desc) > 20000 {
		p.Desc = string([]rune(p.Desc)[:min(5000, len([]rune(p.Desc)))])
	}
	text = p.Title + "\n作者：" + p.Owner.Name + "\n时长：" + strconv.Itoa(p.Duration) + " 秒\n\n" + p.Desc
	result := resultFor("bilibili-info", text)
	result.Type = "fields"
	result.Title = p.Title
	result.Description = p.Desc
	result.Fields = fields
	result.Links = []Link{{"在 B 站打开", "https://www.bilibili.com/video/" + p.BVID}}
	return result, nil
}

func bilibiliTask() AsyncTool {
	tool, _ := findTool("bilibili-info")
	return AsyncTool{Title: tool.Title, Validate: func(args Arguments) error { _, err := bilibiliID(args.Text); return err }, Execute: func(ctx context.Context, args Arguments) (Result, error) { return ReadBilibili(ctx, args.Text, nil) }}
}
