// Package menud 是 menu 域的装配声明包。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
package menud

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
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
	// Store 菜单仓储（InitBridges 的 repos.Menu 同一实例，无双构造）。
	Store func() *store.Store[authmodel.Menu]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 menu 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain；Services 委托 secretgrpc.NewMenuServer——传输适配
// 不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *menubiz.Biz
	dom := domains.StandardDomain("menu", "menud",
		func(context.Context, *appkit.App) error {
			st, err := domains.RequireStore("menud", "menu store", d.Store())
			if err != nil {
				return err
			}
			biz = menubiz.New(st)
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("menud", h)
			if err != nil {
				return err
			}
			hgin.RegisterMenu(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
	dom.Services = func(s *grpc.Server) {
		menuv1.RegisterMenuServiceServer(s, secretgrpc.NewMenuServer(biz))
	}
	return dom
}
