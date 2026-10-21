// Package apiserver 是 bald-admin 的 HTTP 服务装配根（server 层）。
//
// 本层只剩**非域共面**的挂载（RegisterBaseFaces）：
// 21 域的路由/gRPC 服务全部由各自域声明包
// （internal/apiserver/domains/<d>d）的 Routes/Services 注册——appkit 在业务
// BeforeStart 之后逐域调用（声明序=执行序），域路由随 WithHTTP 声明的
// *gin.Engine 挂载。认证/授权依赖由装配层注入，不在此写业务。
package apiserver

import (
	gingonic "github.com/gin-gonic/gin"

	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	"github.com/kalandramo/bald-admin/internal/apiserver/middleware/apiaudit"
)

// RegisterBaseFaces 挂载**非域共面**：api 类审计中间件（源 ApiAuditLog）+
// service info + openapi。生产 BeforeStart 与 handler 级 e2e 共用同一装配路径
// ——中间件在此（而非 main.go 的 router）挂载保证两者行为一致。
//
// 时序契约：gin 的 Use 只影响其后注册的路由——本函数必须在域路由注册**之前**
// 调用。生产路径上它住业务 BeforeStart，而域 Routes 由 appkit 在业务 BeforeStart
// 之后的域装配钩子里逐域调用（bald/pkg/appkit/domain.go 时序窗口），故共面
// 中间件覆盖全部域路由；e2e 侧先 RegisterBaseFaces 再调 hgin.Register* 即同序。
//
// 认证器约束归各域声明包：域路由所需的带吊销检查认证器
// 由装配层构造并经 Deps getter 注入（tenantd/authd 的域头注）。
func RegisterBaseFaces(e *gingonic.Engine) {
	e.Use(apiaudit.Middleware())
	hgin.RegisterServiceInfo(e)
	hgin.RegisterOpenAPI(e)
}
