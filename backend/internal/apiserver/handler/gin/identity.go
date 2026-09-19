// Package gin 提供 identity 扩展域的 HTTP handler（Wave 1.6，协议层）。
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

	identitybiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/identity"
)

// RegisterIdentity 挂载 identity 扩展域路由（Wave 1.6）。
//
// 覆盖源 user_credential（10 rpc）+ login_policy（6 rpc）+ user_profile（2 rpc 源空实现）。
// 全部经 authzMW 保护——凭证与登录策略是**安全敏感**资源，仅管理员可操作。
func RegisterIdentity(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *identitybiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	op := func(c *gingonic.Context) (tenantID, userID string) {
		claims := authn.AuthClaimsFromContext(c.Request.Context())
		if claims == nil {
			return "", ""
		}
		return claims.TenantID, claims.Subject
	}

	// ---- user_credential ----

	authed.POST("/credentials", authzMW, func(c *gingonic.Context) {
		tid, _ := op(c)
		var req struct {
			UserID         string `json:"user_id"`
			IdentityType   string `json:"identity_type"`
			Identifier     string `json:"identifier"`
			CredentialType string `json:"credential_type"`
			Password       string `json:"password"` // 明文（biz 层强制哈希）
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreateCredential(c.Request.Context(), identitybiz.Credential{
			UserID: req.UserID, TenantID: tid, IdentityType: req.IdentityType,
			Identifier: req.Identifier, CredentialType: req.CredentialType,
		}, req.Password)
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/credentials", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListCredentials(c.Request.Context(), c.Query("user_id"))
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/credentials/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetCredential(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.GET("/credentials/by-identifier", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetCredentialByIdentifier(c.Request.Context(),
			c.Query("identity_type"), c.Query("identifier"))
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.DELETE("/credentials/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeleteCredential(c.Request.Context(), c.Param("id")); err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// 凭证校验（源 VerifyCredential）——**公开端点**：登录流程用，此时无会话。
	e.POST("/v1/credentials/verify", func(c *gingonic.Context) {
		var req struct {
			IdentityType string `json:"identity_type"`
			Identifier   string `json:"identifier"`
			Password     string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		valid, userID, err := biz.VerifyCredential(c.Request.Context(),
			req.IdentityType, req.Identifier, req.Password)
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		// 恒 200：有效/无效都是业务结果（与 ValidateToken 同语义）。
		c.JSON(http.StatusOK, gingonic.H{"valid": valid, "user_id": userID})
	})

	// 修改凭证（需旧密码）。
	authed.POST("/credentials/change", authzMW, func(c *gingonic.Context) {
		var req struct {
			IdentityType string `json:"identity_type"`
			Identifier   string `json:"identifier"`
			OldPassword  string `json:"old_password"`
			NewPassword  string `json:"new_password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.ChangeCredential(c.Request.Context(), req.IdentityType,
			req.Identifier, req.OldPassword, req.NewPassword); err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "changed"})
	})

	// 重置凭证（不需旧密码——权限由 authz 严格限制）。
	authed.POST("/credentials/reset", authzMW, func(c *gingonic.Context) {
		var req struct {
			IdentityType string `json:"identity_type"`
			Identifier   string `json:"identifier"`
			NewPassword  string `json:"new_password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		if err := biz.ResetCredential(c.Request.Context(), req.IdentityType,
			req.Identifier, req.NewPassword); err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "reset"})
	})

	// ---- login_policy ----

	authed.POST("/login-policies", authzMW, func(c *gingonic.Context) {
		tid, uid := op(c)
		var req struct {
			TargetID string `json:"target_id"`
			Type     string `json:"type"`
			Method   string `json:"method"`
			Value    string `json:"value"`
			Reason   string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindErr(c, err)
			return
		}
		res, err := biz.CreatePolicy(c.Request.Context(), tid, uid, identitybiz.Policy{
			TargetID: req.TargetID, Type: req.Type, Method: req.Method,
			Value: req.Value, Reason: req.Reason,
		})
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, res)
	})

	authed.GET("/login-policies", authzMW, func(c *gingonic.Context) {
		tid, _ := op(c)
		items, err := biz.ListPolicies(c.Request.Context(), tid)
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"items": items})
	})

	authed.GET("/login-policies/count", authzMW, func(c *gingonic.Context) {
		tid, _ := op(c)
		n, err := biz.CountPolicies(c.Request.Context(), tid)
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"total": n})
	})

	authed.GET("/login-policies/:id", authzMW, func(c *gingonic.Context) {
		res, err := biz.GetPolicy(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})

	authed.DELETE("/login-policies/:id", authzMW, func(c *gingonic.Context) {
		if err := biz.DeletePolicy(c.Request.Context(), c.Param("id")); err != nil {
			writeIdentityErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gingonic.H{"message": "deleted"})
	})

	// ---- user_profile（源空实现 → 501）----
	unimpl := func(name string) gingonic.HandlerFunc {
		return func(c *gingonic.Context) {
			web.ErrorResponse(c, berrors.Unimplemented("identity/not_implemented").
				WithMessage("%s: not implemented in source project", name))
		}
	}
	authed.POST("/profile/bind-contact", authzMW, unimpl("BindContact"))
	authed.POST("/profile/verify-contact", authzMW, unimpl("VerifyContact"))
}

// writeIdentityErr 映射 identity 域错误。
func writeIdentityErr(c *gingonic.Context, err error) {
	switch {
	case errors.Is(err, identitybiz.ErrUnimplemented):
		web.ErrorResponse(c, berrors.Unimplemented("identity/not_implemented").WithMessage("%s", err))
	case errors.Is(err, identitybiz.ErrNotFound):
		web.ErrorResponse(c, berrors.NotFound("identity/not_found").WithMessage("%s", err))
	case errors.Is(err, identitybiz.ErrConflict):
		web.ErrorResponse(c, berrors.AlreadyExists("identity/conflict").WithMessage("%s", err))
	case errors.Is(err, identitybiz.ErrValidation):
		web.ErrorResponse(c, berrors.BadRequest("identity/invalid_request").WithMessage("%s", err))
	default:
		// 校验失败等归 400（biz 层的 fmt.Errorf 无哨兵，按语义归类）。
		writeBizErr(c, err)
	}
}
