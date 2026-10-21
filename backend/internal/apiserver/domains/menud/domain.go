// Package menud 是 menu 域的装配声明包。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
package menud

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"

	gingonic "github.com/gin-gonic/gin"
	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
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
	// Store 菜单仓储（InitBridges 的 repos.Menu 同一实例，无双构造）。
	Store func() *store.Store[authmodel.Menu]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 menu 域的装配声明值（Routes 委托 hgin.RegisterMenu、Services
// 委托 secretgrpc.NewMenuServer——传输适配不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *menubiz.Biz
	return appkit.Domain{
		Name: "menu",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st := d.Store()
			if st == nil {
				return fmt.Errorf("menud: menu store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = menubiz.New(st)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("menud: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterMenu(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			menuv1.RegisterMenuServiceServer(s, secretgrpc.NewMenuServer(biz))
		},
	}
}
