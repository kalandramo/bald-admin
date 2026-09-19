// Package gin 提供站内消息域的 HTTP handler（Wave 2.5）。
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

	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
)

// RegisterMessage 挂载站内消息路由（Wave 2.5，源 24 rpc）。
//
// 权限设计：
//   - 管理面（消息/分类的 CRUD、发送、撤销）→ 仅 admin；
//   - **收件箱相关（列表/已读/删除）→ 任何登录用户**（操作的是自己的收件箱）。
//     这一点与源一致：源从 auth.FromContext 取 operator，只能操作本人收件箱。
func RegisterMessage(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *msgbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)
	op := func(c *gingonic.Context) (tenantID, userID, name string) {
		if claims := authn.AuthClaimsFromContext(c.Request.Context()); claims != nil {
			return claims.TenantID, claims.Subject, claims.Name
		}
		return "", "", ""
	}

	// ---- message ----

	authed.POST("/messages", authzMW, func(c *gingonic.Context) {
		tid, uid, name := op(c)
		var req msgbiz.Message
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateMessage(c.Request.Context(), tid, uid, name, req)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/messages", authzMW, func(c *gingonic.Context) {
		tid, _, _ := op(c)
		items, err := biz.ListMessages(c.Request.Context(), tid)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	// 发送（三模式：target_all / recipient_user_id / target_user_ids）。
	authed.POST("/messages/send", authzMW, func(c *gingonic.Context) {
		tid, uid, name := op(c)
		var req struct {
			Title           string   `json:"title"`
			Content         string   `json:"content"`
			Type            string   `json:"type"`
			CategoryID      string   `json:"category_id"`
			TargetAll       bool     `json:"target_all"`
			RecipientUserID string   `json:"recipient_user_id"`
			TargetUserIDs   []string `json:"target_user_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.SendMessage(c.Request.Context(), tid, uid, name, msgbiz.SendRequest{
			Title: req.Title, Content: req.Content, Type: req.Type,
			CategoryID: req.CategoryID, TargetAll: req.TargetAll,
			RecipientUserID: req.RecipientUserID, TargetUserIDs: req.TargetUserIDs,
		})
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	// 撤销（源 RevokeMessage，按 message_id + 可选 user_id）。
	authed.POST("/messages/:id/revoke", authzMW, func(c *gingonic.Context) {
		var req struct {
			UserID string `json:"user_id"`
		}
		_ = c.ShouldBindJSON(&req)
		if err := biz.RevokeMessage(c.Request.Context(), c.Param("id"), req.UserID); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "revoked"})
	})

	authed.GET("/messages/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetMessage(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/messages/:id", authzMW, func(c *gingonic.Context) {
		var req msgbiz.Message
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateMessage(c.Request.Context(), c.Param("id"), req); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/messages/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteMessage(c.Request.Context(), c.Param("id")); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- category ----

	authed.POST("/message-categories", authzMW, func(c *gingonic.Context) {
		tid, _, _ := op(c)
		var req msgbiz.Category
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateCategory(c.Request.Context(), tid, req)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/message-categories", authzMW, func(c *gingonic.Context) {
		tid, _, _ := op(c)
		items, err := biz.ListCategories(c.Request.Context(), tid)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/message-categories/count", authzMW, func(c *gingonic.Context) {
		tid, _, _ := op(c)
		n, err := biz.CountCategories(c.Request.Context(), tid)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	authed.GET("/message-categories/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetCategory(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.PUT("/message-categories/:id", authzMW, func(c *gingonic.Context) {
		var req msgbiz.Category
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.UpdateCategory(c.Request.Context(), c.Param("id"), req); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "updated"})
	})

	authed.DELETE("/message-categories/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteCategory(c.Request.Context(), c.Param("id")); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- 收件箱（**任何登录用户操作自己的**）----

	// 收件箱列表：从认证上下文取当前用户，不接受 user_id 参数——
	// 避免越权读他人收件箱（源同此：operator 从 ctx 取）。
	authed.GET("/inbox", authzMW, func(c *gingonic.Context) {
		_, uid, _ := op(c)
		items, err := biz.ListUserInbox(c.Request.Context(), uid)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/inbox/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetRecipient(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.POST("/inbox/:id/read", authzMW, func(c *gingonic.Context) {
		if err := biz.MarkAsRead(c.Request.Context(), c.Param("id")); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "marked as read"})
	})

	authed.POST("/inbox/mark-status", authzMW, func(c *gingonic.Context) {
		var req struct {
			IDs    []string `json:"ids"`
			Status string   `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.MarkStatus(c.Request.Context(), req.IDs, req.Status); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "ok"})
	})

	authed.DELETE("/inbox/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteFromInbox(c.Request.Context(), c.Param("id")); err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	authed.GET("/recipients/count", authzMW, func(c *gingonic.Context) {
		tid, _, _ := op(c)
		n, err := biz.CountRecipients(c.Request.Context(), tid)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	// 按 id 批量查询（源 GetInternalMessageRecipientsByIds）。
	authed.POST("/recipients/by-ids", authzMW, func(c *gingonic.Context) {
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		items, err := biz.GetRecipientsByIDs(c.Request.Context(), req.IDs)
		if err != nil {
			writeMsgErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})
}

// writeMsgErr 映射站内消息域错误。
func writeMsgErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, msgbiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("message/not_found").WithMessage("%s", err))
	case errors.Is(err, msgbiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("message/conflict").WithMessage("%s", err))
	case errors.Is(err, msgbiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("message/invalid_request").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
