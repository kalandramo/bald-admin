// Package gin 提供组织架构域的 HTTP handler（Wave 1.7）。
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

	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
)

// RegisterOrg 挂载组织架构路由（Wave 1.7）：
// org_unit（7 rpc，树形）+ position（7 rpc，关联回填）。
func RegisterOrg(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *orgbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)
	tid := func(c *gingonic.Context) string {
		if claims := authn.AuthClaimsFromContext(c.Request.Context()); claims != nil {
			return claims.TenantID
		}
		return ""
	}

	// ---- org_unit ----

	authed.POST("/org-units", authzMW, func(c *gingonic.Context) {
		var req orgbiz.OrgUnit
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateOrgUnit(c.Request.Context(), tid(c), req)
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/org-units", authzMW, func(c *gingonic.Context) {
		tree, err := biz.ListOrgUnits(c.Request.Context(), tid(c))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": tree})
	})

	authed.GET("/org-units/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.CountOrgUnits(c.Request.Context(), tid(c))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	authed.GET("/org-units/:code", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetOrgUnit(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/org-units/:code", authzMW, func(c *gingonic.Context) {
		var req orgbiz.OrgUnit
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateOrgUnit(c.Request.Context(), tid(c), c.Param("code"), req); err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/org-units/:code", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteOrgUnit(c.Request.Context(), tid(c), c.Param("code")); err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	authed.POST("/org-units/batch", authzMW, func(c *gingonic.Context) {
		var req struct {
			Items []orgbiz.OrgUnit `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		created, failed := biz.BatchCreateOrgUnits(c.Request.Context(), tid(c), req.Items)
		c.JSON(http.StatusOK, gingonic.H{"created": created, "failed": failed})
	})

	// ---- position ----

	authed.POST("/positions", authzMW, func(c *gingonic.Context) {
		var req orgbiz.Position
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreatePosition(c.Request.Context(), tid(c), req)
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/positions", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListPositions(c.Request.Context(), tid(c))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/positions/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.CountPositions(c.Request.Context(), tid(c))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	authed.GET("/positions/:code", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetPosition(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/positions/:code", authzMW, func(c *gingonic.Context) {
		var req orgbiz.Position
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdatePosition(c.Request.Context(), tid(c), c.Param("code"), req); err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/positions/:code", authzMW, func(c *gingonic.Context) {
		if err := biz.DeletePosition(c.Request.Context(), tid(c), c.Param("code")); err != nil {
			writeOrgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	authed.POST("/positions/batch", authzMW, func(c *gingonic.Context) {
		var req struct {
			Items []orgbiz.Position `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		created, failed := biz.BatchCreatePositions(c.Request.Context(), tid(c), req.Items)
		c.JSON(http.StatusOK, gingonic.H{"created": created, "failed": failed})
	})
}

// writeOrgErr 映射组织架构域错误。
func writeOrgErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, orgbiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("org/not_found").WithMessage("%s", err))
	case errors.Is(err, orgbiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("org/conflict").WithMessage("%s", err))
	case errors.Is(err, orgbiz.ErrCycle):
		web.ErrorResponse(c, berrors.BadRequest("org/cycle").WithMessage("%s", err))
	case errors.Is(err, orgbiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("org/invalid_request").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
