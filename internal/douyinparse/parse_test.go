package douyinparse

import "testing"

func TestSharePage(t *testing.T) {
	const id = "7692776036492273326"
	good := []byte(`<script>window._ROUTER_DATA = {"loaderData":{"video_(id)/page":{"videoInfoRes":{"item_list":[{"aweme_id":"7692776036492273326","desc":"公开测试","author":{"nickname":"作者"},"video":{"play_addr":{"url_list":["https://aweme.snssdk.com/aweme/v1/playwm/?video_id=sample"]}}}]}}}}</script>`)
	v, err := Parse(good, id)
	if err != nil || v.Title != "公开测试" || v.URL != "https://aweme.snssdk.com/aweme/v1/play/?video_id=sample" {
		t.Fatalf("%+v %v", v, err)
	}
	for _, b := range [][]byte{[]byte("captcha"), []byte(`<script>window._ROUTER_DATA = {"loaderData":{}}</script>`), []byte(`<script>window._ROUTER_DATA = invalid</script>`)} {
		if _, e := Parse(b, id); e == nil {
			t.Fatal("empty/challenge accepted")
		}
	}
	if _, e := Parse(good, "6961737553342991651"); e == nil {
		t.Fatal("wrong post accepted")
	}
}
