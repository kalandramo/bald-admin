// Package gin 提供 go-bald-admin 的 HTTP handler 装配（协议层）。
//
// 本文件演示 M1 认证授权范式：用 bald 的 gin 中间件
// （middleware/gin.AuthnMiddleware / AuthzMiddleware）保护路由。业务经由 biz 层
// 调用，handler 仅做协议转换与中间件接线，不写入认证/授权策略。
package gin

import (
	"errors"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	web "github.com/kalandramo/bald/transport/web"

	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
)

// RegisterAuth 挂载认证与受限资源路由。
//   - POST /v1/login            公开
//   - GET  /v1/auth/whoami      需认证（任意已登录用户）→ 权限点 "auth:get"
//   - GET  /v1/secret/:id       需认证 + "secret:get"（viewer/admin）
//   - DEL  /v1/secret/:id       需认证 + "secret:delete"（仅 admin）
func RegisterAuth(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *authbiz.Biz,
	secretBiz *secretbiz.SecretBiz,
) {
	e.POST("/v1/login", func(c *gingonic.Context) {
		var cred authbiz.Credential
		if err := c.ShouldBindJSON(&cred); err != nil {
			bindErr(c, err)
			return
		}
		// T6 登录审计：客户端信息 handler 层提取（biz 保持协议无关）。
		cred.ClientIP = c.ClientIP()
		cred.UserAgent = c.Request.UserAgent()
		pair, err := biz.Login(c.Request.Context(), cred)
		if err != nil {
			// 仅凭据错误归 401（reason=BAD_CREDENTIAL 供前端程序化识别）；查询/签发等
			// 内部错误归 500——此前一刀切 401 会把 DB 故障伪装成"密码错误"。
			if errors.Is(err, authbiz.ErrBadCredential) {
				web.ErrorResponse(c, berrors.Unauthenticated("auth/bad_credential").WithMessage("%s", err))
				return
			}
			// Wave 1a：限流归 429，与凭据错误区分——前端据此提示「尝试过于频繁」
			// 而非「密码错」。用 ResourceExhausted（berrors 无 TooManyRequests 工厂；
			// httperr.go:33 将其映射为 HTTP 429，语义注释即「资源耗尽（限流）」）。
			if errors.Is(err, authbiz.ErrRateLimited) {
				web.ErrorResponse(c, berrors.ResourceExhausted("auth/rate_limited").WithMessage("%s", err))
				return
			}
			// Wave 1b：登录依赖（DB）熔断打开 → 503（服务暂不可用），
			// 而非 500——语义是「暂时不可用，稍后重试」，不是「内部错误」。
			if errors.Is(err, authbiz.ErrLoginUnavailable) {
				web.ErrorResponse(c, berrors.Unavailable("auth/login_unavailable").WithMessage("%s", err))
				return
			}
			writeBizErr(c, err)
			return
		}
		c.JSON(http.StatusOK, pair)
	})

	// Wave 1d：RefreshToken 是**公开端点**（不要求 access_token）。
	//
	// 为什么不能在需认证组：refresh 的用途正是「access_token 已过期时换新的」。
	// 若要求先认证（有效 access_token），逻辑上循环——access_token 没过期根本
	// 不需要刷新。refresh_token **本身就是凭证**（且是一次性的，见 biz 层
	// ConsumeRefresh 的 GETDEL 语义）。
	e.POST("/v1/auth/refresh", func(c *gingonic.Context) {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		pair, err := biz.RefreshToken(c.Request.Context(), req.RefreshToken)
		if err != nil {
			// 刷新令牌无效归 401（与凭据错误同类：都是「你的凭证不被接受」）。
			if errors.Is(err, authbiz.ErrRefreshInvalid) {
				web.ErrorResponse(c, berrors.Unauthenticated("auth/invalid_refresh_token").WithMessage("%s", err))
				return
			}
			writeBizErr(c, err)
			return
		}
		c.JSON(http.StatusOK, pair)
	})

	// 需认证分组。
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	// Authz 由路由级中间件按 (资源名, 动作) 判定；归一化由核心拦截器完成（P9 反哺）：
	// HTTP 侧经 DefaultHTTPObject/DefaultHTTPAction 把 path/method 翻译为与 gRPC 同源的权限点。
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	// Wave 1d：Logout 在需认证组内——它需要从 ctx 取当前 token 才能拉黑。
	// 跳过 authzMW：登出是「对自己 token 的操作」，不属于任何业务资源权限点
	//（源项目同样不为其定义 RBAC 权限）。认证是唯一门槛。
	authed.POST("/auth/logout", func(c *gingonic.Context) {
		if err := biz.Logout(c.Request.Context()); err != nil {
			writeBizErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "logged out"})
	})

	// ValidateToken 是公开端点（无需认证）——它的用途正是让**未持有有效会话**的
	// 调用方（如网关、前端）询问「这个 token 还有效吗」。若要求先认证再校验，
	// 逻辑上循环（能通过认证就说明有效，无需再问）。
	e.POST("/v1/auth/validate", func(c *gingonic.Context) {
		var req struct {
			Token string `json:"token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		// 恒 200：有效/无效都是**业务结果**（valid 字段），不是错误码——
		// 让调用方能区分「token 无效」与「校验服务故障」。
		c.JSON(http.StatusOK, biz.ValidateToken(c.Request.Context(), req.Token))
	})

	authed.GET("/auth/whoami", authzMW, func(c *gingonic.Context) {
		info, err := biz.WhoAmI(c.Request.Context())
		if err != nil {
			writeBizErr(c, err) // NotFound（认证后用户被删/停用）→404、内部→500
			return
		}
		c.JSON(http.StatusOK, info)
	})

	authed.GET("/secret/:id", authzMW, func(c *gingonic.Context) {
		// M6.3：经真实 DAL 读取，自动受 ctx 租户隔离约束；越权跨租户检索被 store 拦为 404。
		item, err := secretBiz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404（含跨租户）、缓存/内部→500
			return
		}
		c.JSON(http.StatusOK, item)
	})

	authed.DELETE("/secret/:id", authzMW, func(c *gingonic.Context) {
		// M6 CR 修复：真实删除，经 biz 落 store，自动受 ctx 租户隔离约束；
		// 跨租户/不存在返回 404（与 Get 一致）。
		ok, err := secretBiz.Delete(c.Request.Context(), c.Param("id"))
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
