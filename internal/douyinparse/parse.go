// Package douyinparse contains the single-video share-page extraction from
// github.com/wujunwei928/parse-video v0.0.2, parser/douyin.go (MIT, copyright
// 2022 wujunwei928). See licenses/parse-video-MIT.txt.
// Adaptation: pure parsing only, with explicit missing/deleted/album rejection;
// HTTP sessions, cancellation and downloads belong to Haven's caller.
package douyinparse

import (
	"bytes"
	"errors"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

var routerData = regexp.MustCompile(`(?s)window\._ROUTER_DATA\s*=\s*(.*?)</script>`)

type Video struct{ ID, Title, Author, URL string }

func Parse(body []byte, expectedID string) (Video, error) {
	findRes := routerData.FindSubmatch(body)
	if len(findRes) < 2 {
		return Video{}, errors.New("分享页未返回作品数据，可能受到平台限制。")
	}
	jsonBytes := bytes.TrimSpace(findRes[1])
	if !gjson.ValidBytes(jsonBytes) {
		return Video{}, errors.New("分享页数据格式已变化。")
	}
	data := gjson.GetBytes(jsonBytes, "loaderData.video_(id)/page.videoInfoRes.item_list.0")
	if !data.Exists() {
		return Video{}, errors.New("作品详情为空：可能已删除、非公开或受到平台限制。")
	}
	if data.Get("aweme_id").String() != expectedID {
		return Video{}, errors.New("分享页返回的作品与链接不一致。")
	}
	if len(data.Get("images").Array()) > 0 {
		return Video{}, errors.New("这是一条图集；当前工具仅下载单个视频。")
	}
	// This is the upstream extraction, not a pixel-editing watermark remover.
	videoURL := data.Get("video.play_addr.url_list.0").String()
	videoURL = strings.ReplaceAll(videoURL, "playwm", "play")
	if videoURL == "" {
		return Video{}, errors.New("平台没有提供可下载的视频源。")
	}
	return Video{ID: expectedID, Title: data.Get("desc").String(), Author: data.Get("author.nickname").String(), URL: videoURL}, nil
}
