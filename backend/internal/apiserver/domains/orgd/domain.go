// Package orgd 是 org 域的装配声明包。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 差异两处：① biz 构造收**三仓储**（org_unit 树 + position + user——user 仓储
// 与 userd 共享 InitBridges 同一实例，是装配层注入而非域间依赖）；
// ② gRPC 面是**双 service**（OrgUnit + Position 共用同一 org biz）——Services
// 一次回调登记两条。
package orgd

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
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
	// OrgUnitStore 组织单元仓储（InitBridges 的 repos.OrgUnit 同一实例）。
	OrgUnitStore func() *store.Store[authmodel.OrgUnit]
	// PositionStore 岗位仓储（repos.Position）。
	PositionStore func() *store.Store[authmodel.Position]
	// UserStore 用户仓储（repos.User——与 userd/装配层共享同一实例，非域间依赖）。
	UserStore func() *store.Store[authmodel.User]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 org 域的装配声明值（Routes 委托 hgin.RegisterOrg、Services 委托
// secretgrpc.NewOrgServer/NewPositionServer——传输适配不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *orgbiz.Biz
	return appkit.Domain{
		Name: "org",
		Wire: func(_ context.Context, _ *appkit.App) error {
			ous, err := domains.RequireStore("orgd", "org unit store", d.OrgUnitStore())
			if err != nil {
				return err
			}
			ps, err := domains.RequireStore("orgd", "position store", d.PositionStore())
			if err != nil {
				return err
			}
			us, err := domains.RequireStore("orgd", "user store", d.UserStore())
			if err != nil {
				return err
			}
			biz = orgbiz.New(ous, ps, us)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("orgd", h)
			if err != nil {
				return err
			}
			hgin.RegisterOrg(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			identityv1.RegisterOrgUnitServiceServer(s, secretgrpc.NewOrgServer(biz))
			identityv1.RegisterPositionServiceServer(s, secretgrpc.NewPositionServer(biz))
		},
	}
}
