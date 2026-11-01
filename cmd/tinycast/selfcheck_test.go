package main

import (
	"strings"
	"testing"
)

func TestCallbackPortProblem(t *testing.T) {
	cases := []struct {
		name     string
		authAddr string
		httpAddr string
		want     string
	}{
		{"端口一致", "http://127.0.0.1:18855/internal/mtx/auth", ":18855", ""},
		{"容器服务名也只看端口", "http://tinycast:18855/internal/mtx/auth", "0.0.0.0:18855", ""},
		{"端口不一致", "http://127.0.0.1:8080/internal/mtx/auth", ":18855", "MediaMTX 会回调 8080"},
		{"缺省 http 端口", "http://127.0.0.1/internal/mtx/auth", ":80", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callbackPortProblem(tc.authAddr, tc.httpAddr)
			if tc.want == "" && got != "" {
				t.Fatalf("不应报告问题，实际 %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("提示应包含 %q，实际 %q", tc.want, got)
			}
		})
	}
}
