// middleware.go 分组认证中间件的统一装配点。
//
// 为什么收敛到 helper：本包各 Register* 各自 authed.Use(authnMiddleware(...))
// 在 main 期（RegisterRoutes）执行，而 Authn 缺省在构造时刻快照
// 全局 auditor（bald pkg/middleware/authn.go）——快照发生在契约轨
// BeforeStart 装配与 R1-2 协调器热切之前，必为 nop，401 认证失败审计
// 全部静默丢失（违约：认证失败必须显式留痕）。统一注入
// securityaudit.Global() 动态转发器（每次 Record 读全局）后，两条轨的
// 后端切换对已构造中间件即时生效（框架使用文档《Bald 审计使用》§3c）。
package gin

import (
	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mw "github.com/kalandramo/bald/pkg/middleware"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// authnMiddleware 构造带动态审计转发器的分组认证中间件
// （与 main.go 两处 bundle.Audit(securityaudit.Global()) 同一转发器，
// 保证请求审计与认证失败审计同源）。
func authnMiddleware(a authn.Authenticator) gingonic.HandlerFunc {
	return mw.GinHandler(mw.Authn(a, mw.AuthnWithAuditor(securityaudit.Global())))
}

// auditMiddleware 构造分组请求审计中间件。**必须挂在 authnMiddleware 之后、
// authz 之前**：统一 Audit 在 next 之前从 ctx 读 AuthClaims（v0.16.0 语义，
// 旧 ginmw 实现是 next 之后回读 gin context），挂在外面读不到认证产物——
// subject 恒空、tenant 恒走兜底。挂在 authz 之前：授权拒绝的 berrors
// PermissionDenied 会穿过本层，Audit 据此记 ResultDeny。
//
// 与 authnMiddleware 同一动态转发器（securityaudit.Global()），后端热切
// 即时生效。
func auditMiddleware() gingonic.HandlerFunc {
	auditH := mw.GinHandler(mw.Audit(
		mw.AuditWithAuditor(securityaudit.Global()),
		mw.AuditWithObjectResolver(authz.DefaultHTTPObjectFromOperation),
		mw.AuditWithActionResolver(authz.DefaultHTTPActionFromOperation),
	))
	return func(c *gingonic.Context) {
		// 客户端信息注入请求 ctx：统一 Audit 的 Meta 不含 ip/ua，由落库
		// 汇聚点（securityaudit.GlobalAuditor.enrichClientInfo）读取补齐。
		// ip 用 gin 的 ClientIP（含可信代理头解析，与旧 ginmw 审计同语义）。
		ctx := securityaudit.WithClientInfo(c.Request.Context(), c.ClientIP(), c.Request.UserAgent())
		c.Request = c.Request.WithContext(ctx)
		auditH(c)
	}
}
