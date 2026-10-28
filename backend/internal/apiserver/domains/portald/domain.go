// Package portald 是 portal 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：proto（api/gen/go/admin 的 admin_portal，pb.gw 转码注解）仅作 REST 面类型来源，无 gRPC server 实现、无网关登记——Services 不声明（合法）。仓储与 user/role/permission/menu 域同一实例（无双构造）。
package portald

import (
	"context"
	"net/http"

	portalbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/portal"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
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
	// UserStore User 仓储（InitBridges 的 repos.User 同一实例，无双构造）。
	UserStore func() *store.Store[authmodel.User]
	// RoleStore Role 仓储（InitBridges 的 repos.Role 同一实例，无双构造）。
	RoleStore func() *store.Store[authmodel.Role]
	// PermissionStore Permission 仓储（InitBridges 的 repos.Permission 同一实例，无双构造）。
	PermissionStore func() *store.Store[authmodel.Permission]
	// MenuStore Menu 仓储（InitBridges 的 repos.Menu 同一实例，无双构造）。
	MenuStore func() *store.Store[authmodel.Menu]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 portal 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain）：routes 委托 hgin.RegisterAdminPortal——传输适配
// 不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *portalbiz.Biz
	return domains.StandardDomain("portal", "portald",
		func(context.Context, *appkit.App) error {
			us, err := domains.RequireStore("portald", "store", d.UserStore())
			if err != nil {
				return err
			}
			rs, err := domains.RequireStore("portald", "store", d.RoleStore())
			if err != nil {
				return err
			}
			ps, err := domains.RequireStore("portald", "store", d.PermissionStore())
			if err != nil {
				return err
			}
			ms, err := domains.RequireStore("portald", "store", d.MenuStore())
			if err != nil {
				return err
			}
			biz = portalbiz.New(us, rs, ps, ms)
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("portald", h)
			if err != nil {
				return err
			}
			hgin.RegisterAdminPortal(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
}
