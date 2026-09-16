// middleware.go 分组认证中间件的统一装配点。
//
// 为什么收敛到 helper：本包 9 个 Register* 各自 authed.Use(mid.AuthnMiddleware(...))
// 在 main 期（RegisterRoutes）执行，而 AuthnMiddleware 缺省在构造时刻快照
// 全局 auditor（bald pkg/middleware/gin/authn.go）——快照发生在契约轨
// BeforeStart 装配与 R1-2 协调器热切之前，必为 nop，401 认证失败审计
// 全部静默丢失（D3 违约：认证失败必须显式留痕）。统一注入
// securityaudit.Global() 动态转发器（每次 Record 读全局）后，两条轨的
// 后端切换对已构造中间件即时生效（框架使用文档《Bald 审计使用》§3c）。
package gin

import (
	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// authnMiddleware 构造带动态审计转发器的分组认证中间件
// （与 main.go 两处 bundle.Audit(securityaudit.Global()) 同一转发器，
// 保证请求审计与认证失败审计同源）。
func authnMiddleware(a authn.Authenticator) gingonic.HandlerFunc {
	return mid.AuthnMiddleware(a, mid.AuthnWithAuditor(securityaudit.Global()))
}
