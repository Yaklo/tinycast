// Package version 汇总构建期注入的版本信息。
//
// 发布构建通过 go build -ldflags "-X tinycast/internal/version.Version=..." 注入，
// 直接 go build 时保留默认值；缺失的字段会回落到 Go 构建信息里的 VCS 元数据。
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// 只接受 go install tinycast@v0.1.0 这种干净的模块版本；
// 本地 go build 会合成 v0.0.0-<时间>-<提交>+dirty 这样的伪版本，对用户没有意义。
var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// 这三个变量由构建脚本用 -ldflags -X 覆盖，必须是 string 且被下面的函数引用，
// 否则链接器可能把符号丢掉，注入会静默失效。
var (
	Version   = "devel"
	Commit    = ""
	BuildTime = ""
)

// Info 是结构化的版本信息，直接作为 /api/version 的 JSON 输出。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	GoVersion string `json:"goVersion"`
	Dirty     bool   `json:"dirty,omitempty"`
}

// Current 返回当前二进制的版本信息。
func Current() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}

	if meta, ok := debug.ReadBuildInfo(); ok {
		for _, s := range meta.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.BuildTime == "" {
					info.BuildTime = s.Value
				}
			case "vcs.modified":
				info.Dirty = s.Value == "true"
			}
		}
		// go install tinycast@v0.1.0 时 Go 会写上模块版本，本地构建是 (devel)。
		if info.Version == "devel" && releaseVersion.MatchString(meta.Main.Version) {
			info.Version = meta.Main.Version
		}
	}

	if len(info.Commit) > 7 {
		info.Commit = info.Commit[:7]
	}
	// 自己编的二进制没有 tag，若工作区有改动，补一个 -dirty 后缀，避免和提交版混淆。
	if info.Dirty && !strings.HasSuffix(info.Version, "-dirty") {
		info.Version += "-dirty"
	}
	return info
}

// String 返回便于粘贴到 issue 里的单行版本串，例如
// v0.1.0 (rev b6da756, 2026-11-02T03:00:00Z, go1.24.7)。
func String() string {
	return format(Current())
}

func format(i Info) string {
	parts := make([]string, 0, 3)
	if i.Commit != "" {
		parts = append(parts, "rev "+i.Commit)
	}
	if i.BuildTime != "" {
		parts = append(parts, i.BuildTime)
	}
	parts = append(parts, i.GoVersion)
	return i.Version + " (" + strings.Join(parts, ", ") + ")"
}
