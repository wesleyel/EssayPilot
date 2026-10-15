// Package web 是内嵌的前端（原生 JS），由 server 以静态文件提供。
package web

import "embed"

//go:embed *.html *.css *.js
var FS embed.FS
