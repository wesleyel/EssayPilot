// EssayPilot：考研作文批阅工作台。单个二进制，数据存一个 SQLite 文件。
// 服务端不调用模型；评分、润色由本地 Agent 经 MCP（/mcp）或浏览器 WebMCP 完成。
package main

import (
	"crypto/subtle"
	"embed"
	"flag"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8790", "监听地址")
	dbPath := flag.String("db", "essaypilot.db", "SQLite 数据文件")
	mcpOn := flag.Bool("mcp", false, "开启 MCP（Streamable HTTP，路径 /mcp）")
	token := flag.String("token", os.Getenv("ESSAYPILOT_TOKEN"), "访问令牌（Bearer）；监听非本机地址时必填，也可用环境变量 ESSAYPILOT_TOKEN")
	trial := flag.String("webmcp-token", "", "Chrome WebMCP origin trial 令牌（可选）")
	webDir := flag.String("web", "", "开发用：从该目录读取前端，代替内嵌文件")
	flag.Parse()

	if !loopback(*addr) && *token == "" {
		log.Fatalf("监听 %s 不是本机地址，请用 -token 设置访问令牌", *addr)
	}

	svc, err := NewService(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Close()

	mux := http.NewServeMux()
	mux.Handle("/api/", newAPI(svc, APIOptions{MCPEnabled: *mcpOn, MCPPath: "/mcp", WebMCP: true}))
	if *mcpOn {
		mux.Handle("/mcp", newMCPHandler(svc))
	}
	sub, _ := fs.Sub(webFS, "web")
	if *webDir != "" {
		sub = os.DirFS(*webDir)
	}
	mux.Handle("/", staticHandler(sub, *trial))

	log.Printf("EssayPilot  http://%s", *addr)
	if *mcpOn {
		log.Printf("MCP         http://%s/mcp", *addr)
	}
	log.Fatal(http.ListenAndServe(*addr, withToken(*token, mux)))
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
