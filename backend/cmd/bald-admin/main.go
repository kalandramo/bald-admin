// Command bald-admin 是 bald-admin 服务入口。
//
// 本文件只做入口：构造并执行根命令，出错以非零码退出（供脚本判断服务状态）。
// 全部装配逻辑在同目录的 app/ 子包（对齐 miniblog 的 cmd/mb-apiserver 形态）。
//
// 运行：
//
//	go run ./cmd/bald-admin --config=configs/bald-admin.yaml
//	go run ./cmd/bald-admin --server.http.addr=:18080
//	BALD_ADMIN_SERVER_HTTP_ADDR=:18080 go run ./cmd/bald-admin
//	（env 前缀由 app name 规范化派生：bald-admin → BALD_ADMIN_，下划线即点路径分隔）
//
//	# 验证 HTTP 路由
//	curl -i http://127.0.0.1:8080/v1/ping
//	curl -i http://127.0.0.1:8080/v1/info
//
// 设计见 docs/设计文档.md；分层参考 osbuilder 脚手架模板。
package main

import (
	"os"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app"
)

func main() {
	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
