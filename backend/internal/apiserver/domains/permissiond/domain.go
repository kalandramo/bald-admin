// Package permissiond 是 permission 域的装配声明包（批 4 第二波，机械域）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
package permissiond

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"

	gingonic "github.com/gin-gonic/gin"
	permissionv1 "github.com/kalandramo/bald-admin/api/gen/go/permission/v1"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// Store 权限仓储（InitBridges 的 repos.Permission 同一实例，无双构造）。
	Store func() *store.Store[authmodel.Permission]
	// PolicyStore 角色策略仓储（repos.RolePolicy——permission biz 的策略评估面）。
	PolicyStore func() *store.Store[authmodel.RolePolicy]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 permission 域的装配声明值（Routes 委托 hgin.RegisterPermission、
// Services 委托 secretgrpc.NewPermissionServer——传输适配不搬家，机械迁移）。
func Domain(d Deps) appkit.Domain {
	var biz *permissionbiz.Biz
	return appkit.Domain{
		Name: "permission",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st := d.Store()
			if st == nil {
				return fmt.Errorf("permissiond: permission store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = permissionbiz.New(st, d.PolicyStore())
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("permissiond: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterPermission(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			permissionv1.RegisterPermissionServiceServer(s, secretgrpc.NewPermissionServer(biz))
		},
	}
}
