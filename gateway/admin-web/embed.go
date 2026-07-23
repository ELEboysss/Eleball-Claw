// Package adminweb 内嵌 claw 管理后台构建产物（admin-web/dist），供 claw-server 单文件分发。
// package 名用 adminweb（import 路径含 - 的 admin-web 不能作包名）。
package adminweb

import "embed"

//go:embed all:dist
var DistFS embed.FS
