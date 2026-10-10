// Package serviceinfo 是 M0 最小业务域，演示 biz 层（use case）分层：
// 业务函数与传输层解耦，gin handler 与后续 gRPC handler 共用同一份 biz。
//
// 命名说明：本域提供**服务自述**端点（/v1/ping、/v1/info），与框架的
// `bald/health`（探针聚合：/healthz、/readyz，经 appkit.WithHealth 装配）
// 是**两回事**。此处刻意不叫 health——避免 grep「health」时把「探针」
// 与「服务自述」混为一谈（二者曾同名，读代码时极易误判）。
package serviceinfo

import "context"

// Info 返回服务基本信息（M0 占位，后续从配置/注册表读取）。
func Info(_ context.Context) map[string]string {
	return map[string]string{
		"service": "bald-admin",
		"version": "v0.1.0",
		"status":  "ok",
	}
}

// Ping 返回存活应答（M0 占位；**不是**健康探针——探针见 bald/health）。
func Ping(_ context.Context) string {
	return "pong"
}
