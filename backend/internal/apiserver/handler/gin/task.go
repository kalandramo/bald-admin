// Package gin 提供任务调度域的 HTTP handler（Wave 2.3）。
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

	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
)

// RegisterTask 挂载任务调度路由（Wave 2.3，源 11 rpc）。
func RegisterTask(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *taskbiz.Biz,
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

	authed.POST("/tasks", authzMW, func(c *gingonic.Context) {
		var req taskbiz.Task
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateTask(c.Request.Context(), tid(c), req)
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/tasks", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListTasks(c.Request.Context(), tid(c))
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/tasks/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.CountTasks(c.Request.Context(), tid(c))
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	// 已注册的处理器类型（源 ListTaskTypeName）。
	authed.GET("/tasks/types", authzMW, func(c *gingonic.Context) {
		types := biz.ListTaskTypeNames(c.Request.Context())
		c.JSON(http.StatusOK, gingonic.H{"type_names": types})
	})

	// 全部启动 / 停止 / 重启（源 StartAllTask / StopAllTask / RestartAllTask）。
	authed.POST("/tasks/start-all", authzMW, func(c *gingonic.Context) {
		n, err := biz.StartAllTasks(c.Request.Context())
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"started": n})
	})

	authed.POST("/tasks/stop-all", authzMW, func(c *gingonic.Context) {
		biz.StopAllTasks(c.Request.Context())
		c.JSON(http.StatusOK, gingonic.H{"message": "stopped"})
	})

	authed.POST("/tasks/restart-all", authzMW, func(c *gingonic.Context) {
		n, err := biz.RestartAllTasks(c.Request.Context())
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"restarted": n})
	})

	authed.GET("/tasks/:type_name", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetTask(c.Request.Context(), c.Param("type_name"))
		if err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/tasks/:type_name", authzMW, func(c *gingonic.Context) {
		var req taskbiz.Task
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateTask(c.Request.Context(), c.Param("type_name"), req); err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/tasks/:type_name", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteTask(c.Request.Context(), c.Param("type_name")); err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// 单任务启停（源 ControlTask）。
	authed.POST("/tasks/:type_name/control", authzMW, func(c *gingonic.Context) {
		var req struct {
			Start bool `json:"start"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.ControlTask(c.Request.Context(), c.Param("type_name"), req.Start); err != nil {
			writeTaskErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "ok"})
	})
}

// writeTaskErr 映射任务域错误。
func writeTaskErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, taskbiz.ErrNoScheduler):
		// 调度器未配置 → 503（能力不可用，非请求错误）——
		// 与源「明确报错而非静默」的语义一致。
		web.ErrorResponse(c, berrors.Unavailable("task/scheduler_unavailable").WithMessage("%s", err))
	case errors.Is(err, taskbiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("task/not_found").WithMessage("%s", err))
	case errors.Is(err, taskbiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("task/conflict").WithMessage("%s", err))
	case errors.Is(err, taskbiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("task/invalid_request").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
