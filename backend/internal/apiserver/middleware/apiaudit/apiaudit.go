// Package apiaudit 提供 api 类审计的 gin 中间件（Wave 5.1）。
//
// ## 与框架审计中间件的关系
//
// bald 框架的 `middleware/gin.AuditMiddleware` 发的是**operation 类**事件
// （业务操作审计，object/action 经 P9 归一化）。源 go-wind-admin 的 api 类
// （ApiAuditLog）是**独立的接口调用审计**——记录 HTTP 方法/路径/状态码/耗时/
// 请求体等**协议层**信息，与「谁对什么资源做了什么」的业务语义正交。
//
// 框架中间件的 Meta 已含 status/client_ip/user_agent/request_id/trace_id，
// 但**缺** http_method/path/latency_ms/request_body/response——且 Meta 由框架
// 在中间件内构造，业务无法注入。故本包自建一个轻量中间件，在请求结束时发
// **category=api** 的独立事件（框架中间件不动，两者并存不重复）。
//
// ## 挂载位置
//
// 挂在 `apiserver.RegisterRoutes` 内（生产与 e2e 共用同一装配路径）——
// 若只挂 main.go 的 router，e2e 走 RegisterRoutes 就测不到（分叉即静默失效）。
package apiaudit

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/contextx"
)

// Middleware 返回 api 类审计中间件（旁路，不阻断业务）。
//
// 在 c.Next() 返回后发一条 category=api 事件，Meta 含协议层字段。
// 写入失败由 auditor 自身降级（框架 StoreAuditor 的 fallback 语义），
// 本中间件额外 recover 防护，绝不向上游抛错。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		ev := audit.AuditEvent{
			Time:     start,
			Subject:  subject(c),
			TenantID: tenant(c),
			Object:   c.FullPath(),
			Action:   c.Request.Method,
			Result:   result(c),
			Meta: map[string]any{
				"category":    "api",
				"http_method": c.Request.Method,
				"path":        c.Request.URL.Path,
				"status":      c.Writer.Status(),
				"latency_ms":  time.Since(start).Milliseconds(),
				"client_ip":   c.ClientIP(),
				"user_agent":  c.Request.UserAgent(),
				"request_id":  contextx.RequestIDFromContext(c.Request.Context()),
				"trace_id":    contextx.TraceIDFromContext(c.Request.Context()),
			},
		}
		recordSafely(c.Request.Context(), ev)
	}
}

// subject 从认证上下文取主体（未认证为空）。
func subject(c *gin.Context) string {
	if cl := authn.AuthClaimsFromContext(c.Request.Context()); cl != nil {
		return cl.Subject
	}
	return ""
}

// tenant 从认证上下文取租户。
func tenant(c *gin.Context) string {
	if cl := authn.AuthClaimsFromContext(c.Request.Context()); cl != nil {
		return cl.TenantID
	}
	return ""
}

// result 由响应状态码推导（与框架审计中间件同规则：401/403→deny、>=500→error）。
func result(c *gin.Context) audit.Result {
	switch {
	case c.Writer.Status() == 401 || c.Writer.Status() == 403:
		return audit.ResultDeny
	case c.Writer.Status() >= 500:
		return audit.ResultError
	default:
		return audit.ResultAllow
	}
}

// recordSafely 旁路记录：auditor panic 仅忽略，绝不向上游抛错。
func recordSafely(ctx context.Context, ev audit.AuditEvent) {
	defer func() { _ = recover() }()
	audit.GetAuditor().Record(ctx, ev)
}
