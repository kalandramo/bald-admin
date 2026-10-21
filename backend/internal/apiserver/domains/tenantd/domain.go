// Package tenantd 是 tenant 域的装配声明包。
//
// 为什么不在 biz/v1/tenant 里放 domain.go：Routes/Services 委托既有传输适配
// （handler/gin、handler/grpc），而这两个包 import biz/v1/tenant（handler/gin/
// tenant.go、handler/grpc/tenant.go 的 biz 参数类型）——biz 包再声明域值并
// import handler 即成 import 环。故每域声明值独立成包（internal/apiserver/
// domains/<d>），biz 与 handler 的双向引用在此汇合，依赖方向保持单向：
// 本包 → biz + 传输适配。
//
// 依赖取用约定：
//   - 基础设施件（仓储/认证器/授权器）住装配层：仓储由业务 BeforeStart 的
//     InitBridges 建立、认证/授权由 BeforeStart 构造——域经 Deps 的 getter
//     **迟到取用**。时序依据：域 Wire 晚于业务 BeforeStart（bald/pkg/appkit/
//     domain.go 的时序窗口注释），getter 届时返回已就绪实例；声明期直接取值
//     会固化 nil。不经 App.Resource：仓储非阶段 B Provider 产物，且 App 无
//     运行期资源写入 API（setResources 未导出）；
//   - 域间不互相 import：user 不消费 tenant 的 store/biz（租户隔离在 store 层
//     P8 全局注册 bootstrap.store.RegisterTenant，与 tenant 域无关，租户内
//     读写由 Where.T 自动完成）。若未来 X 域需要 Y 域的业务能力，经 Y 域包
//     导出的访问器 + X 的 Deps getter 注入（域声明序保证 Y 的 Wire 先行），
//     不 import 对方 biz 包构造第二份。
package tenantd

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入）。getter 而非值：依赖在业务
// BeforeStart 才就绪，声明期取值为 nil（同 appOptions 的 bizSet/authenticator
// 函数作用域 + 闭包赋值纪律）。
type Deps struct {
	// Store 租户仓储（InitBridges 的 repos.Tenant 同一实例——无双构造；
	// auth biz 的登录链路共享同一仓储）。
	Store func() *store.Store[authmodel.Tenant]
	// Authn 认证器（带吊销检查，token.NewRevocationChecker；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 tenant 域的装配声明值。Routes 委托 hgin.RegisterTenant、
// Services 委托 secretgrpc.NewTenantServer——传输适配不搬家，路由面/gRPC 面/
// 中间件链不变（策略归一化 "tenant" 仍双协议单写）。
func Domain(d Deps) appkit.Domain {
	var biz *tenantbiz.Biz
	return appkit.Domain{
		Name: "tenant",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st, err := domains.RequireStore("tenantd", "tenant store", d.Store())
			if err != nil {
				return err
			}
			biz = tenantbiz.New(st)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("tenantd", h)
			if err != nil {
				return err
			}
			hgin.RegisterTenant(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			tenantv1.RegisterTenantServiceServer(s, secretgrpc.NewTenantServer(biz))
		},
	}
}
