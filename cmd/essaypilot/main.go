// EssayPilot：考研作文批阅工作台。单个二进制，数据存一个 SQLite 文件。
// 服务端不调用模型；评分、润色由本地 Agent 经 MCP（/mcp）或浏览器 WebMCP 完成。
package main

import (
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/wesleyel/EssayPilot/internal/core"
	"github.com/wesleyel/EssayPilot/internal/server"
	"github.com/wesleyel/EssayPilot/web"
)

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

	svc, err := core.NewService(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Close()

	var webFS fs.FS = web.FS
	if *webDir != "" {
		webFS = os.DirFS(*webDir)
	}
	handler := server.New(svc, server.Options{MCP: *mcpOn, Token: *token, WebMCPToken: *trial, Web: webFS})

	log.Printf("EssayPilot  http://%s", *addr)
	if *mcpOn {
		log.Printf("MCP         http://%s/mcp", *addr)
	}
	log.Fatal(http.ListenAndServe(*addr, handler))
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
