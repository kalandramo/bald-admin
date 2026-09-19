// Package gin 提供 MFA 的 HTTP handler 装配（Wave 1.5，协议层）。
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

	mfabiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/mfa"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
)

// RegisterMFA 挂载 MFA 路由（源 mfa.proto 的 10 个 rpc）。
//
// 全部需认证（MFA 是用户对自己账号的安全设置）。
// authorization 用 (mfa, get/post) 权限点——admin 种子策略需含之（见 seedPolicies）。
//
// **源未实现的 3 条**（StartMFAChallenge/GenerateBackupCodes/ListBackupCodes）
// 也挂路由但返回 501，理由：显式标注「源未实现」比 404 更诚实——调用方能区分
// 「本服务没有这个能力」与「这个能力源项目就没做」。
func RegisterMFA(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *mfabiz.Biz,
) {
	authed := e.Group("/v1/mfa")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	// 当前操作者（tenant + user）从认证上下文取。
	op := func(c *gingonic.Context) (tenantID, userID string) {
		claims := authn.AuthClaimsFromContext(c.Request.Context())
		if claims == nil {
			return "", ""
		}
		return claims.TenantID, claims.Subject
	}

	// GET /v1/mfa/status
	authed.GET("/status", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		st, err := biz.GetStatus(c.Request.Context(), tid, uid)
		if err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, st)
	})

	// GET /v1/mfa/methods
	authed.GET("/methods", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		items, err := biz.ListEnrolled(c.Request.Context(), tid, uid)
		if err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	// POST /v1/mfa/enroll/start
	authed.POST("/enroll/start", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		res, err := biz.StartEnroll(c.Request.Context(), tid, uid)
		if err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	// POST /v1/mfa/enroll/confirm
	authed.POST("/enroll/confirm", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		var req struct {
			OperationID string `json:"operation_id"`
			TOTPCode    string `json:"totp_code"`
			Display     string `json:"display"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		credID, err := biz.ConfirmEnroll(c.Request.Context(), tid, uid, req.OperationID, req.TOTPCode, req.Display)
		if err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"success": true, "credential_id": credID})
	})

	// POST /v1/mfa/disable
	authed.POST("/disable", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		var req struct {
			CredentialID string `json:"credential_id"`
			UserID       string `json:"user_id"` // 管理端指定他人（源语义）
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		target := uid
		if req.UserID != "" {
			target = req.UserID // 管理端重置（源允许，权限由 authz 挡）
		}
		if err := biz.Disable(c.Request.Context(), tid, target, req.CredentialID); err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "disabled"})
	})

	// POST /v1/mfa/device/revoke
	authed.POST("/device/revoke", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		var req struct {
			CredentialID string `json:"credential_id"`
			UserID       string `json:"user_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		target := uid
		if req.UserID != "" {
			target = req.UserID
		}
		if err := biz.RevokeDevice(c.Request.Context(), tid, target, req.CredentialID); err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "revoked"})
	})

	// ---- 源未实现的 3 条：显式 501（对齐源行为，不自创）----
	unimpl := func(name string) gingonic.HandlerFunc {
		return func(c *gingonic.Context) {
			web.ErrorResponse(c, berrors.Unimplemented("mfa/not_implemented").
				WithMessage("%s: not implemented in source project", name))
		}
	}
	authed.POST("/challenge/start", authzMW, unimpl("StartMFAChallenge"))
	authed.POST("/backup-codes/generate", authzMW, unimpl("GenerateBackupCodes"))
	authed.GET("/backup-codes", authzMW, unimpl("ListBackupCodes"))
	authed.POST("/backup-codes", authzMW, unimpl("ListBackupCodes"))

	// POST /v1/mfa/challenge/verify —— 登录挑战验证（源真实现）。
	// **公开端点**：登录流程中密码校验通过后、尚未持有效会话时调用。
	// 故不挂 authnMiddleware——凭 operation_id 单次有效 + 归属校验兜底（源同此）。
	e.POST("/v1/mfa/challenge/verify", func(c *gingonic.Context) {
		var req struct {
			OperationID string `json:"operation_id"`
			TOTPCode    string `json:"totp_code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		_, err := biz.VerifyChallenge(c.Request.Context(), req.OperationID, req.TOTPCode)
		if err != nil {
			writeMFAErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"success": true})
	})
}

// writeMFAErr 映射 MFA 错误。
func writeMFAErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, mfabiz.ErrUnimplemented):
		web.ErrorResponse(c, berrors.Unimplemented("mfa/not_implemented").WithMessage("%s", err))
	case errors.Is(err, mfa.ErrEnrollExists):
		web.ErrorResponse(c, berrors.AlreadyExists("mfa/already_enrolled").WithMessage("%s", err))
	case errors.Is(err, mfa.ErrOperationInvalid):
		web.ErrorResponse(c, berrors.BadRequest("mfa/invalid_operation").WithMessage("%s", err))
	case errors.Is(err, mfa.ErrUserMismatch):
		web.ErrorResponse(c, berrors.PermissionDenied("mfa/user_mismatch").WithMessage("%s", err))
	case errors.Is(err, mfabiz.ErrTooFrequent):
		web.ErrorResponse(c, berrors.ResourceExhausted("mfa/too_frequent").WithMessage("%s", err))
	case errors.Is(err, mfa.ErrInvalidCode):
		web.ErrorResponse(c, berrors.Unauthenticated("mfa/invalid_code").WithMessage("%s", err))
	default:
		writeBizErr(c, err)
	}
}
