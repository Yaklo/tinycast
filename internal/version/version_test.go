package version

import (
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "完整信息",
			in:   Info{Version: "v0.1.0", Commit: "b6da756", BuildTime: "2026-11-02T03:00:00Z", GoVersion: "go1.24.7"},
			want: "v0.1.0 (rev b6da756, 2026-11-02T03:00:00Z, go1.24.7)",
		},
		{
			name: "缺少 VCS 信息",
			in:   Info{Version: "v0.1.0", GoVersion: "go1.24.7"},
			want: "v0.1.0 (go1.24.7)",
		},
		{
			name: "开发构建",
			in:   Info{Version: "devel", Commit: "abc1234", GoVersion: "go1.24.7"},
			want: "devel (rev abc1234, go1.24.7)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := format(c.in); got != c.want {
				t.Fatalf("format() = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestCurrentUsesInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldTime := Version, Commit, BuildTime
	t.Cleanup(func() { Version, Commit, BuildTime = oldVersion, oldCommit, oldTime })

	Version = "v9.9.9"
	Commit = "abcdef0123456789"
	BuildTime = "2026-11-02T03:00:00Z"

	info := Current()
	if !strings.HasPrefix(info.Version, "v9.9.9") {
		t.Fatalf("Version = %q，期望以 v9.9.9 开头", info.Version)
	}
	if info.Commit != "abcdef0" {
		t.Fatalf("Commit = %q，期望截断成 7 位", info.Commit)
	}
	if info.BuildTime != "2026-11-02T03:00:00Z" {
		t.Fatalf("BuildTime = %q，注入值被覆盖了", info.BuildTime)
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Fatalf("GoVersion = %q，期望以 go 开头", info.GoVersion)
	}
}

// 没有被 -X 注入时也不能出现空版本。
func TestStringNotEmpty(t *testing.T) {
	if got := String(); got == "" {
		t.Fatal("String() 返回空串")
	}
}
