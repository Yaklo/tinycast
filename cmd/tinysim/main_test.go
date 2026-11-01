package main

import (
	"net/url"
	"regexp"
	"testing"
	"time"
)

// 推流参数要能原样穿过 RTMP 查询串，中文与 & 都不能被吞掉。
func TestParamRoundTrip(t *testing.T) {
	s := &sim{room: "深夜 & 写代码", token: "AbC-dEf_1234567890123456789012"}
	q := url.Values{}
	q.Set("room", s.room)
	q.Set("token", s.token)
	raw := q.Encode()

	want := "room=%E6%B7%B1%E5%A4%9C+%26+%E5%86%99%E4%BB%A3%E7%A0%81&token=AbC-dEf_1234567890123456789012"
	if raw != want {
		t.Fatalf("编码结果 = %q，期望 %q", raw, want)
	}

	parsed, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.Get("room") != s.room {
		t.Errorf("房间名还原失败：%q", parsed.Get("room"))
	}
	if parsed.Get("token") != s.token {
		t.Errorf("令牌还原失败：%q", parsed.Get("token"))
	}
}

func TestGenerateTokenMatchesServerPattern(t *testing.T) {
	pattern := regexp.MustCompile("^[A-Za-z0-9_-]{16,256}$")
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		token := generateToken()
		if !pattern.MatchString(token) {
			t.Fatalf("令牌 %q 不符合服务端 tokenPattern", token)
		}
		if seen[token] {
			t.Fatalf("生成了重复令牌 %q", token)
		}
		seen[token] = true
	}
}

func TestRTMPBase(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080": "rtmp://127.0.0.1:1935/live",
		"http://localhost:8080": "rtmp://localhost:1935/live",
	}
	for in, want := range cases {
		if got := rtmpBase(in); got != want {
			t.Errorf("rtmpBase(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("  ", "x") != "x" {
		t.Error("空白应回退到默认值")
	}
	if orDefault("a", "x") != "a" {
		t.Error("非空值应保留")
	}
}

func TestPauseDisabled(t *testing.T) {
	s := &sim{step: 0}
	start := time.Now()
	s.pause("不应等待")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("step<=0 时不应等待，实际耗时 %s", elapsed)
	}
}
