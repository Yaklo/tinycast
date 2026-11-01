package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestVersionEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	rec, body := do(t, srv, http.MethodGet, "/api/version", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	got, _ := body["version"].(string)
	if got == "" {
		t.Fatalf("version 字段为空: %v", body)
	}
	goVersion, _ := body["goVersion"].(string)
	if !strings.HasPrefix(goVersion, "go") {
		t.Fatalf("goVersion = %q，期望以 go 开头", goVersion)
	}
}
