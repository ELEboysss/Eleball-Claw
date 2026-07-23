// Package web 内嵌 claw 用户端 H5 构建产物（web/dist），供 claw-server 单文件分发。
// dist 由 make build-web / npm run build 生成（plan §A.3 go:embed 单文件分发）。
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
