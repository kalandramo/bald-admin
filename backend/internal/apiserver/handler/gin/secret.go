package gin

import (
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	web "github.com/kalandramo/bald/transport/web"

	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
)

// RegisterSecret 挂载受限资源（secret）路由。批 4 域形态迁移时自 RegisterAuth
// 尾部拆出（原两路由块原样搬迁：路径/方法/中间件链逐行不变）——secret 域迁入
// appkit.Domain 后其 Routes 须独立委托本函数，而 auth 域路由（RegisterAuth
// 其余部分）仍住 server.go，两域路由面必须可分别注册：
//   - GET  /v1/secret/:id   需认证 + "secret:get"（viewer/admin）
//   - DEL  /v1/secret/:id   需认证 + "secret:delete"（仅 admin）
func RegisterSecret(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *secretbiz.SecretBiz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/secret/:id", authzMW, func(c *gingonic.Context) {
		// M6.3：经真实 DAL 读取，自动受 ctx 租户隔离约束；越权跨租户检索被 store 拦为 404。
		item, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404（含跨租户）、缓存/内部→500
			return
		}
		c.JSON(http.StatusOK, item)
	})

	authed.DELETE("/secret/:id", authzMW, func(c *gingonic.Context) {
		// M6 CR 修复：真实删除，经 biz 落 store，自动受 ctx 租户隔离约束；
		// 跨租户/不存在返回 404（与 Get 一致）。
		ok, err := biz.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		if !ok {
			web.ErrorResponse(c, berrors.NotFound("secret/not_found").WithMessage("secret not found"))
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"deleted": c.Param("id")})
	})
}
