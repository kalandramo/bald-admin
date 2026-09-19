// Package gin 提供权限组与策略评估日志的 HTTP handler（Wave 4.1，源 7 rpc）。
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

	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
)

// RegisterPermGroup 挂载权限组与策略评估日志路由（仅 admin）。
//
// 路径对齐源的 HTTP 注解（`/admin/v1/permission-groups` 等），
// 但本项目统一用 `/v1/*` 前缀（与其余域一致，不引入第二前缀）。
func RegisterPermGroup(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *pgbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)
	tenantOf := func(c *gingonic.Context) string {
		if claims := authn.AuthClaimsFromContext(c.Request.Context()); claims != nil {
			return claims.TenantID
		}
		return ""
	}

	// ---- 权限组 ----

	// 创建（body.code 作为路径段，与项目其余域的 code 风格一致）。
	authed.POST("/permission-groups", authzMW, func(c *gingonic.Context) {
		tid := tenantOf(c)
		var req struct {
			Code string `json:"code"`
			pgbiz.Group
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateGroup(c.Request.Context(), tid, req.Code, req.Group)
		if err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	// 树形列表。
	authed.GET("/permission-groups", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListGroupTree(c.Request.Context(), tenantOf(c))
		if err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/permission-groups/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetGroup(c.Request.Context(), c.Param("id"))
		if err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/permission-groups/:id", authzMW, func(c *gingonic.Context) {
		var req pgbiz.Group
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateGroup(c.Request.Context(), c.Param("id"), req); err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/permission-groups/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteGroup(c.Request.Context(), c.Param("id")); err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- 策略评估日志（只读）----

	authed.GET("/policy-evaluation-logs", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListEvalLogs(c.Request.Context(), tenantOf(c))
		if err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/policy-evaluation-logs/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetEvalLog(c.Request.Context(), c.Param("id"))
		if err != nil {
			writePGErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})
}

// writePGErr 映射权限组域错误。
func writePGErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, pgbiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("permission-group/not_found").WithMessage("%s", err))
	case errors.Is(err, pgbiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("permission-group/conflict").WithMessage("%s", err))
	case errors.Is(err, pgbiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("permission-group/invalid_request").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
