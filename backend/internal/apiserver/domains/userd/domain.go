// Package userd 是 user 域的装配声明包。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 与 tenant 的差异仅一处：user 是租户内读写域——隔离不经本域代码，store 层
// P8（Where.T 自动过滤/写入注入）承担；本包 Wire 只构造 biz，无租户感知。
package userd

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
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
	// Store 用户仓储（InitBridges 的 repos.User 同一实例——auth 的登录链路
	// 与 org/dashboard/message/portal 等共享同一仓储，无双构造）。
	Store func() *store.Store[authmodel.User]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 user 域的装配声明值（Routes 委托 hgin.RegisterUser、Services
// 委托 secretgrpc.NewUserServer——传输适配不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *userbiz.Biz
	return appkit.Domain{
		Name: "user",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st, err := domains.RequireStore("userd", "user store", d.Store())
			if err != nil {
				return err
			}
			biz = userbiz.New(st)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("userd", h)
			if err != nil {
				return err
			}
			hgin.RegisterUser(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			userv1.RegisterUserServiceServer(s, secretgrpc.NewUserServer(biz))
		},
	}
}
