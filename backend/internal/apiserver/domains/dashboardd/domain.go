// Package dashboardd 是 dashboard 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无 proto 纯 gin 面；审计仓储与 auditlog 域同一实例（无双构造）。
package dashboardd

import (
	"context"
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	dashbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dashboard"
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
	// AuditStore AuditRecord 仓储（InitBridges 的 repos.Audit 同一实例，无双构造）。
	AuditStore func() *store.Store[authmodel.AuditRecord]
	// RoleStore Role 仓储（InitBridges 的 repos.Role 同一实例，无双构造）。
	RoleStore func() *store.Store[authmodel.Role]
	// UserStore User 仓储（InitBridges 的 repos.User 同一实例，无双构造）。
	UserStore func() *store.Store[authmodel.User]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 dashboard 域的装配声明值。Routes 委托 hgin.RegisterDashboard——传输适配
// 不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *dashbiz.Biz
	return appkit.Domain{
		Name: "dashboard",
		Wire: func(_ context.Context, _ *appkit.App) error {
		as := d.AuditStore()
		rs := d.RoleStore()
		us := d.UserStore()
			if as == nil || rs == nil || us == nil {
				return fmt.Errorf("dashboardd: store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = dashbiz.New(as, rs, us)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("dashboardd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterDashboard(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
