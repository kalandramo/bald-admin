// Package identityd 是 identity 域的装配声明包（批 4 第三波，机械域）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
//
// 特殊点：无 proto 纯 gin 面（Services 不声明，合法）；仓储全为 InitBridges 产物。
package identityd

import (
	"context"
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	identitybiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/identity"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps——域 Wire 晚于
// 业务 BeforeStart 执行，届时仓储/认证器均已就绪）。
type Deps struct {
	// CredentialStore UserCredential 仓储（InitBridges 的 repos.Credential 同一实例，无双构造）。
	CredentialStore func() *store.Store[authmodel.UserCredential]
	// LoginPolicyStore LoginPolicy 仓储（InitBridges 的 repos.LoginPolicy 同一实例，无双构造）。
	LoginPolicyStore func() *store.Store[authmodel.LoginPolicy]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 identity 域的装配声明值。Routes 委托 hgin.RegisterIdentity——传输适配
// 不搬家，路由面/中间件链与迁移前逐行等价；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *identitybiz.Biz
	return appkit.Domain{
		Name: "identity",
		Wire: func(_ context.Context, _ *appkit.App) error {
		cs := d.CredentialStore()
		lps := d.LoginPolicyStore()
			if cs == nil || lps == nil {
				return fmt.Errorf("identityd: store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = identitybiz.New(cs, lps)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("identityd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterIdentity(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
