// Package gin 提供套餐三件套的 HTTP handler（Wave 4.2，源 14 rpc）。
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

	planbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/plan"
)

// RegisterPlan 挂载套餐三件套路由（仅 admin——套餐配置是管理面功能）。
func RegisterPlan(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *planbiz.Biz,
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

	// ---- Plan ----

	authed.POST("/plans", authzMW, func(c *gingonic.Context) {
		var req struct {
			Code string `json:"code"`
			planbiz.Plan
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreatePlan(c.Request.Context(), tenantOf(c), req.Code, req.Plan)
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/plans", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListPlans(c.Request.Context(), tenantOf(c))
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/plans/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetPlan(c.Request.Context(), c.Param("id"))
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/plans/:id", authzMW, func(c *gingonic.Context) {
		var req planbiz.Plan
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdatePlan(c.Request.Context(), c.Param("id"), req); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	// 删除套餐 —— **级联删其 modules/quotas**（源 ent Cascade 语义）。
	authed.DELETE("/plans/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeletePlan(c.Request.Context(), c.Param("id")); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- PlanModule ----

	authed.POST("/plan-modules", authzMW, func(c *gingonic.Context) {
		var req struct {
			PlanID string `json:"plan_id"`
			Module string `json:"module"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateModule(c.Request.Context(), tenantOf(c), req.PlanID, req.Module)
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	// 列表按 plan_id 过滤（源 List 同语义）。
	authed.GET("/plan-modules", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListModules(c.Request.Context(), c.Query("plan_id"))
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/plan-modules/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetModule(c.Request.Context(), c.Param("id"))
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/plan-modules/:id", authzMW, func(c *gingonic.Context) {
		var req struct {
			Module string `json:"module"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateModule(c.Request.Context(), c.Param("id"), req.Module); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/plan-modules/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteModule(c.Request.Context(), c.Param("id")); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- PlanQuota（源无 Get，只有 List/Create/Update/Delete）----

	authed.POST("/plan-quotas", authzMW, func(c *gingonic.Context) {
		var req struct {
			PlanID     string `json:"plan_id"`
			QuotaType  string `json:"quota_type"`
			QuotaValue uint64 `json:"quota_value"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateQuota(c.Request.Context(), tenantOf(c), req.PlanID, req.QuotaType, req.QuotaValue)
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/plan-quotas", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListQuotas(c.Request.Context(), c.Query("plan_id"))
		if err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.PUT("/plan-quotas/:id", authzMW, func(c *gingonic.Context) {
		var req struct {
			QuotaValue uint64 `json:"quota_value"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateQuota(c.Request.Context(), c.Param("id"), req.QuotaValue); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/plan-quotas/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteQuota(c.Request.Context(), c.Param("id")); err != nil {
			writePlanErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})
}

func writePlanErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, planbiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("plan/not_found").WithMessage("%s", err))
	case errors.Is(err, planbiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("plan/conflict").WithMessage("%s", err))
	case errors.Is(err, planbiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("plan/invalid_request").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
