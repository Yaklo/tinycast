package httpapi

import (
	"net/http"
	"net/url"
	"testing"

	"tinycast/internal/hub"
)

// 前端对房间名做了双重编码（OBS 会先解一次），后端要多解一层才能还原展示名。
func TestSRSPublishDecodesDoubleEncodedRoom(t *testing.T) {
	srv, h := newFixture(t, nil)

	doubleEncoded := url.QueryEscape(url.QueryEscape("深夜写代码 2 点"))
	body := callbackBody{
		Action:   actionPublish,
		ClientID: "c1",
		App:      "live",
		Stream:   "alice",
		Param:    "?room=" + doubleEncoded + "&token=" + aliceToken,
	}
	rec := postCallback(t, srv, testSecret, body)
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	view, ok := h.Room("alice")
	if !ok {
		t.Fatal("应创建房间")
	}
	if view.Room != "深夜写代码 2 点" {
		t.Fatalf("房间名应被还原成原始展示名，实际 %q", view.Room)
	}
	if view.Warning != "" {
		t.Fatalf("正常推流不应带告警，实际 %q", view.Warning)
	}
}

// 流名里带着查询串 = SRS 没能解析推流 URL（房间名含未编码字符），
// 此时观众端会永久黑屏，必须让页面拿到告警码以便提示主播重新复制推流码。
func TestSRSPublishFlagsUnparsedStream(t *testing.T) {
	srv, h := newFixture(t, nil)

	body := callbackBody{
		Action:   actionPublish,
		ClientID: "c1",
		App:      "live",
		Stream:   "alice?room=测试&token=" + aliceToken,
		Param:    "",
	}
	rec := postCallback(t, srv, testSecret, body)
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("应放行（房间仍可用，只是观众拿不到画面），实际 %d/%q", rec.Code, rec.Body.String())
	}
	view, ok := h.Room("alice")
	if !ok {
		t.Fatal("应创建房间")
	}
	if view.Warning != hub.WarnStreamUnparsed {
		t.Fatalf("应标记 %s，实际 %q", hub.WarnStreamUnparsed, view.Warning)
	}
	if view.Room != "测试" {
		t.Fatalf("仍应解出房间名，实际 %q", view.Room)
	}
}

// 重新推流（推流码修正后）应清掉告警。
func TestSRSPublishClearsWarningOnRepublish(t *testing.T) {
	srv, h := newFixture(t, nil)

	broken := callbackBody{
		Action:   actionPublish,
		ClientID: "c1",
		App:      "live",
		Stream:   "alice?room=测试&token=" + aliceToken,
		Param:    "",
	}
	if rec := postCallback(t, srv, testSecret, broken); rec.Body.String() != "0" {
		t.Fatalf("首次推流应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	postCallback(t, srv, testSecret, callbackBody{Action: actionUnpublish, ClientID: "c1", App: "live", Stream: "alice"})

	if rec := postCallback(t, srv, testSecret, publishBody("alice", "测试", aliceToken, "c2")); rec.Body.String() != "0" {
		t.Fatalf("重新推流应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	view, ok := h.Room("alice")
	if !ok || view.Warning != "" {
		t.Fatalf("重新推流后告警应清空，实际 %+v ok=%v", view, ok)
	}
}

func TestDecodeRoomValue(t *testing.T) {
	cases := map[string]string{
		"测试":                             "测试",
		"%E6%B5%8B%E8%AF%95":             "测试",
		"%25E6%25B5%258B%25E8%25AF%2595": "测试",
		"a b":                            "a b",
		"a+b":                            "a b",
		" 轻量 直播 ":                        "轻量 直播",
		"100%":                           "100%",
		"%zz":                            "%zz",
		"":                               "",
	}
	for in, want := range cases {
		if got := decodeRoomValue(in); got != want {
			t.Errorf("decodeRoomValue(%q) = %q，期望 %q", in, got, want)
		}
	}
}
