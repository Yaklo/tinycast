package main

import (
	"strings"
	"testing"

	"tinycast/internal/config"
)

func internalAddrConfig(internalAddr, secret string) *config.Config {
	return &config.Config{
		HTTPAddr:     "127.0.0.1:18855",
		InternalAddr: internalAddr,
		MediaMTX:     config.MediaMTXConfig{AuthSecret: secret},
	}
}

// 内部回调端口对外监听又没有共享密钥时，同网段任何人都能伪造推流身份：
// 这种配置必须启动失败，而不是打一条 ERROR 日志就继续跑。
func TestValidateInternalAddrRejectsPublicListenerWithoutSecret(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:18856", ":18856", "[::]:18856", "192.0.2.10:18856"} {
		err := validateInternalAddr(internalAddrConfig(addr, ""))
		if err == nil {
			t.Fatalf("INTERNAL_ADDR=%s 且无密钥时应拒绝启动", addr)
		}
		if !strings.Contains(err.Error(), "MEDIAMTX_AUTH_SECRET") {
			t.Fatalf("错误信息应提示配置密钥，实际 %v", err)
		}
	}
}

// 地址写错时报错要指向地址本身，而不是误导人去查密钥。
func TestValidateInternalAddrReportsMalformedAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "localhost", "::1"} {
		err := validateInternalAddr(internalAddrConfig(addr, ""))
		if err == nil {
			t.Fatalf("INTERNAL_ADDR=%q 缺少端口时应报错", addr)
		}
		if !strings.Contains(err.Error(), "host:port") {
			t.Fatalf("错误信息应提示 host:port 形式，实际 %v", err)
		}
	}
}

// 显式把回调地址配成公网端口等于把 /internal/ 重新暴露出去，必须拒绝启动；
// 想共用端口只能留空 INTERNAL_ADDR。
func TestValidateInternalAddrRejectsSameAsHTTP(t *testing.T) {
	cases := []struct{ httpAddr, internalAddr string }{
		{"127.0.0.1:18855", "127.0.0.1:18855"},
		{"0.0.0.0:18855", ":18855"},
		{"[::]:18855", "0.0.0.0:18855"},
	}
	for _, tc := range cases {
		cfg := internalAddrConfig(tc.internalAddr, "s3cret")
		cfg.HTTPAddr = tc.httpAddr
		err := validateInternalAddr(cfg)
		if err == nil {
			t.Fatalf("HTTP_ADDR=%s INTERNAL_ADDR=%s 应拒绝启动", tc.httpAddr, tc.internalAddr)
		}
		if !strings.Contains(err.Error(), "同一个监听地址") {
			t.Fatalf("错误信息应说明地址冲突，实际 %v", err)
		}
	}
}

// 回环地址、未配置、以及配了密钥的情况都应照常启动。
func TestValidateInternalAddrAllowsLoopbackAndSecret(t *testing.T) {
	cases := []struct{ addr, secret string }{
		{"", ""},                       // 未配置：回调沿用 HTTP_ADDR，保持原行为
		{"127.0.0.1:18856", ""},        // 同机 MediaMTX 的默认部署
		{"[::1]:18856", ""},            // IPv6 回环
		{"localhost:18856", ""},        // 解析到回环的主机名
		{"192.0.2.10:18856", "s3cret"}, // 有密钥时由 authSecretOK 兜底
	}
	for _, tc := range cases {
		if err := validateInternalAddr(internalAddrConfig(tc.addr, tc.secret)); err != nil {
			t.Fatalf("INTERNAL_ADDR=%q secret=%q 应允许启动，实际 %v", tc.addr, tc.secret, err)
		}
	}
	if err := validateInternalAddr(nil); err != nil {
		t.Fatalf("nil 配置不应 panic，实际 %v", err)
	}
}
