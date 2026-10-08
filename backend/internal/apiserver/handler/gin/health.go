package gin

import (
	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/health"
)

// RegisterHealth 把 health 业务域暴露为 HTTP 路由（薄适配层）。
// 真实校验/错误映射由 web 与 berrors 统一处理；M0 仅做最简演示。
//
// CORS 不在此挂载：跨域是**应用级**横切关注点，统一由全局中间件链提供
// （bundle.Gin() 的 cors 层，配置源 server.http.middleware.cors）。
// 此前该组自带 mid.CORS(mid.DefaultCORS())——是**仅此一处**的例外（其余 20 个
// 路由组均无 CORS），造成「只有 /v1/ping 与 /v1/info 带 CORS 头」的不一致；
// 更糟的是，即使全局配了 cors 段，该组响应仍被这里的硬编码默认值覆盖
// （配置看起来生效、实际失效）。移除后由全局链统一供给。
func RegisterHealth(e *gingonic.Engine) {
	v1 := e.Group("/v1")
	v1.GET("/ping", func(c *gingonic.Context) {
		c.String(200, health.Ping(c.Request.Context()))
	})
	v1.GET("/info", func(c *gingonic.Context) {
		c.JSON(200, health.Info(c.Request.Context()))
	})
}
