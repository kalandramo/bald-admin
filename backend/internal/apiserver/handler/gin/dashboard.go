// Package gin 提供 dashboard 域的 HTTP handler（Wave 3.4，源 4 rpc）。
package gin

import (
	"net/http"
	"strconv"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	dashbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dashboard"
)

// RegisterDashboard 挂载 dashboard 路由（只读聚合，仅 admin）。
//
// 权限：分析页是管理面功能，源同样挂在 admin 服务下——故仅 admin 可访问。
func RegisterDashboard(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *dashbiz.Biz,
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

	// 概览四卡。
	authed.GET("/dashboard/overview", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetOverview(c.Request.Context(), tenantOf(c))
		if err != nil {
			writeDashErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	// 登录趋势（?days=7，缺日补零）。
	authed.GET("/dashboard/login-trend", authzMW, func(c *gingonic.Context) {
		days := 7
		if v := c.Query("days"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
				days = n
			}
		}
		points, err := biz.GetLoginTrend(c.Request.Context(), tenantOf(c), days)
		if err != nil {
			writeDashErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"points": points})
	})

	// 操作 action 分布。
	authed.GET("/dashboard/operation-actions", authzMW, func(c *gingonic.Context) {
		items, err := biz.GetOperationActionDistribution(c.Request.Context(), tenantOf(c))
		if err != nil {
			writeDashErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	// 登录 status 分布。
	authed.GET("/dashboard/login-status", authzMW, func(c *gingonic.Context) {
		items, err := biz.GetLoginStatusDistribution(c.Request.Context(), tenantOf(c))
		if err != nil {
			writeDashErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})
}

func writeDashErr(c *gingonic.Context, err error) {
	writeBizErr(c, err)
}
