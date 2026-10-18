// Package orgd 是 org 域的装配声明包（批 4 第二波，机械域）。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
//
// 差异两处：① biz 构造收**三仓储**（org_unit 树 + position + user——user 仓储
// 与 userd 共享 InitBridges 同一实例，是装配层注入而非域间依赖，符合裁决一）；
// ② gRPC 面是**双 service**（OrgUnit + Position 共用同一 org biz）——Services
// 一次回调登记两条，与迁移前 registerGRPC 的两行逐行等价。
package orgd

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"

	gingonic "github.com/gin-gonic/gin"
	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
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
// secretgrpc.NewOrgServer/NewPositionServer——传输适配不搬家，机械迁移）。
func Domain(d Deps) appkit.Domain {
	var biz *orgbiz.Biz
	return appkit.Domain{
		Name: "org",
		Wire: func(_ context.Context, _ *appkit.App) error {
			ous := d.OrgUnitStore()
			if ous == nil {
				return fmt.Errorf("orgd: org unit store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			ps := d.PositionStore()
			if ps == nil {
				return fmt.Errorf("orgd: position store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			us := d.UserStore()
			if us == nil {
				return fmt.Errorf("orgd: user store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = orgbiz.New(ous, ps, us)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("orgd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
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
