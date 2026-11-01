package httpapi

import (
	"net/http"

	"tinycast/internal/version"
)

// handleVersion 返回构建期注入的版本信息，便于确认「线上跑的是哪一版」。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Current())
}
