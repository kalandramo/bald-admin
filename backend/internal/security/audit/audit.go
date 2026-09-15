// Package audit 是 go-bald-admin 的审计后端桥接（bald audit.Auditor 的装配薄壳）。
//
// 2026-09-15 起三后端实现上移框架：log → bald/pkg/audit（核心 LoggerAuditor）、
// store → bald/contrib/audit-store、stream → bald/contrib/audit-stream。本包
// 保留原构造 API 作薄壳转发，调用方（main.go buildAuditBackend、e2e）零改动；
// 生产直用框架包亦可。
package audit

import (
	"github.com/kalandramo/bald/pkg/audit"
)

// New 返回日志审计后端实例（转发框架核心 LoggerAuditor）。
func New() audit.Auditor { return audit.NewLoggerAuditor() }
