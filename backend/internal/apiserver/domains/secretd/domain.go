// Package secretd 是 secret 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 与 tenant 的差异一处：biz 构造带 Cache-Aside 后端（cache.Cache）——「配置驱动
// RedisCache 优先、env 兼容通道兜底」的抉择在装配层 BeforeStart 完成，域经
// Deps.Cache getter 迟到取用（抉择逻辑住装配层，域只消费产物）。
package secretd

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"

	gingonic "github.com/gin-gonic/gin"
	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/secret/v1"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/cache"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// Store secret 仓储（InitBridges 的 repos.Secret 同一实例，无双构造）。
	Store func() *store.Store[authmodel.Secret]
	// Cache Cache-Aside 后端（装配层已抉择：RedisCache 优先、env 兜底；
	// nil=禁用缓存，biz 内判 nil 直连 store）。
	Cache func() cache.Cache
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 secret 域的装配声明值。Routes 委托 hgin.RegisterSecret（受限资源
// 两路由）、Services 委托 secretgrpc.NewServer——传输适配不搬家。
func Domain(d Deps) appkit.Domain {
	var biz *secretbiz.SecretBiz
	return appkit.Domain{
		Name: "secret",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st := d.Store()
			if st == nil {
				return fmt.Errorf("secretd: secret store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = secretbiz.New(st, d.Cache())
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("secretd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterSecret(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			adminv1.RegisterSecretServiceServer(s, secretgrpc.NewServer(biz))
		},
	}
}
