// Package audit 是 bald-admin 的审计后端桥接（bald audit.Auditor 的装配薄壳）。
//
// 三后端实现住框架包：log → bald/pkg/audit（核心 LoggerAuditor）、
// store → bald/contrib/audit-gorm、stream → bald/contrib/audit-stream。本包
// 保留构造 API 作薄壳转发，调用方（main.go buildAuditBackend、e2e）零改动；
// 生产直用框架包亦可。
package audit

import (
	"context"
	"sync"

	"github.com/kalandramo/bald/pkg/audit"
)

// ---- 当前生效审计器（生效集由本包自持） ----
//
// 审计生效集由本应用自持（Set/Current）：GlobalAuditor 每次转发 Current()，
// 「中间件构造时刻」与「后端装配时刻」的解耦语义不变。

var (
	currentMu  sync.RWMutex
	currentAud audit.Auditor = audit.NopAuditor()
)

// SetCurrent 注入当前生效审计器（传 nil 回退 nop）。
func SetCurrent(a audit.Auditor) {
	currentMu.Lock()
	defer currentMu.Unlock()
	if a == nil {
		currentAud = audit.NopAuditor()
		return
	}
	currentAud = a
}

// Current 返回当前生效审计器（动态转发语义的读取点）。
func Current() audit.Auditor {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return currentAud
}

// New 返回日志审计后端实例（转发框架核心 LoggerAuditor）。
func New() audit.Auditor { return audit.NewLoggerAuditor() }

// GlobalAuditor 是当前生效审计器的动态转发器：每次 Record 时读 Current()，
// 把「中间件构造时刻」与「后端装配时刻」解耦。
//
// 为什么需要它：bald 的审计/认证中间件在构造时刻绑定 auditor（显式注入
// 优先，否则快照构造时的全局）。而本应用的审计后端在运行期装配与热切换——
// 若中间件直接快照（main 期构造时全局为 nop），请求审计与认证失败审计将
// 静默进 nop。注入本转发器后切换对已挂中间件即时生效。
type GlobalAuditor struct{}

// Global 返回全局审计器动态转发器（可作为 bundle.Audit / AuthnWithAuditor
// / AuditWithAuditor 的注入值）。
func Global() audit.Auditor { return GlobalAuditor{} }

// Record 实现 audit.Auditor：转发当前生效审计器（指针语义切换，无撕裂）。
func (GlobalAuditor) Record(ctx context.Context, ev audit.AuditEvent) {
	Current().Record(ctx, ev)
}
