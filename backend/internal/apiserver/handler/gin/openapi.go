package gin

import (
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver/assets"
)

// RegisterOpenAPI 把内嵌的 OpenAPI v3 契约暴露为 GET /openapi.yaml。
//
// 元端点不进 /v1 业务分组（与 /v1/ping 同级公开）：管理面内网部署，spec
// 本身就是公开 API 契约，无敏感数据。Swagger UI 不内嵌（YAGNI，避免引入
// 前端静态资源依赖）——Apifox 导入或在线 swagger-ui 直接消费本端点。
func RegisterOpenAPI(e *gingonic.Engine) {
	e.GET("/openapi.yaml", func(c *gingonic.Context) {
		// application/yaml：RFC 9519 注册的规范媒体类型。
		c.Data(http.StatusOK, "application/yaml", assets.OpenAPISpec)
	})
}
