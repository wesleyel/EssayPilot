// Package server 把业务层以 HTTP 暴露：REST + SSE（/api）、MCP（/mcp）和前端静态文件。
package server

import (
	"crypto/subtle"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/wesleyel/EssayPilot/internal/core"
)

// Options 是 HTTP 服务的开关。
type Options struct {
	MCP         bool   // 开启 /mcp
	Token       string // 访问令牌；空表示不校验
	WebMCPToken string // Chrome WebMCP origin trial 令牌
	Web         fs.FS  // 前端文件，根目录下有 index.html
}

// New 组装完整的 HTTP 处理器。
func New(s *core.Service, o Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", newAPI(s, APIOptions{MCPEnabled: o.MCP, MCPPath: "/mcp", WebMCP: true}))
	if o.MCP {
		mux.Handle("/mcp", newMCPHandler(s))
	}
	mux.Handle("/", staticHandler(o.Web, o.WebMCPToken))
	return withToken(o.Token, mux)
}

// staticHandler 提供内嵌前端；index.html 里的占位注释替换为 origin-trial meta。
func staticHandler(sub fs.FS, trial string) http.Handler {
	files := http.FileServerFS(sub)
	meta := ""
	if trial != "" {
		meta = fmt.Sprintf(`<meta http-equiv="origin-trial" content="%s">`, html.EscapeString(trial))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			index, err := fs.ReadFile(sub, "index.html")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			page := strings.Replace(string(index), "<!--ORIGIN_TRIAL-->", meta, 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(page))
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// withToken：设置了令牌时，/api 与 /mcp 需要 Authorization: Bearer <token>（SSE 可用 ?token=）。
// 页面本身不拦截，前端从 URL 的 #token= 读取后附在请求上。
func withToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("token")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"未授权"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
