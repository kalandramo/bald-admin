package app

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	filev1 "github.com/kalandramo/bald-admin/api/gen/go/file/v1"
	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	permissionv1 "github.com/kalandramo/bald-admin/api/gen/go/permission/v1"
	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/secret/v1"
	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	validation "github.com/kalandramo/bald-admin/internal/security/validation"
	obmetrics "github.com/kalandramo/bald/contrib/observability-otlp/metrics"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/middleware/bundle"
	grpcmw "github.com/kalandramo/bald/pkg/middleware/grpc"
)

func newGRPCServerOptions(authenticator authn.Authenticator, authorizer authz.Authorizer) []grpc.ServerOption {
	// M10.1（P10 验证）：gRPC 无公开方法（全部需认证），整条链切 bundle——
	// Error→RequestID→Observability→Authn→Audit→Authz 链序由 bundle 固化，
	// 替代此前手写的 7 段拦截器组装（authnInterceptor/authzInterceptor 闭包删除）。
	// P9 归一化经 bundle.Normalized() 内置于 Authz 与 Audit 两层。
	// T6 修复：Audit 层注入动态转发器（同 gin 侧 ginBundle）——此前未注入时
	// bundle 显式接 Nop，请求审计与认证失败审计（bundle 会把 auditor 注入
	// AuthnInterceptor）均静默失效。
	grpcBundle := bundle.New(
		// Wave 4（建议一）：本函数经 appkit.WithGRPCOptions 在**运行期**（Run 期
		// server 构造点、业务 beforeStart 之后）调用——彼时 InitBridges 已建好
		// 真实 Authenticator/Authorizer，故直接持有真实实例，不再需要请求期解析
		// 的 lateAuthn/lateAuthz 间接层。
		bundle.Authn(authenticator),
		bundle.Authz(authorizer),
		bundle.Audit(securityaudit.Global()), // 动态转发：契约轨装配/热切轨切换即时生效
		bundle.Metrics(obmetrics.Recorder("bald/example")),
		bundle.Normalized(), // P9：FullMethod → 与 HTTP 同源的权限点
	)
	// Wave 校验层契约化：把 protovalidate 校验追加到链**尾**（Authz 之后）。
	//
	// 为什么自行追加而非用 GRPCChain()：bundle 不支持注入 validator（其链序
	// 固化为 Error→…→Authz，无校验层位置）。GRPCInterceptors() 是导出的，
	// 取回后追加一层即可——语义正确：先认证授权，再校验参数（未授权请求
	// 不会有机会探测校验规则）。
	//
	// MustNew 在装配期构造：protovalidate.New() 预编译注解里的 CEL 表达式，
	// 注解写错会在此处 panic（fail fast），而非等第一个请求才炸。
	chain := append(grpcBundle.GRPCInterceptors(),
		grpcmw.ValidatorInterceptor(validation.MustNew().Validate))
	return []grpc.ServerOption{grpc.ChainUnaryInterceptor(chain...)}
}

// registerGateway 把 grpc-gateway 的 HTTP handler 注册到 runtime.ServeMux 并交回
// http.Handler（transport.NewGatewayServer 依赖倒置，核心不依赖 grpc-gateway）。
// conn 由 GatewayServer 内部建立（指向本进程 gRPC 服务）。T2 起挂载 tenant/user 网关。

func registerGateway(ctx context.Context, conn *grpc.ClientConn) (http.Handler, error) {
	mux := runtime.NewServeMux()
	if err := adminv1.RegisterSecretServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := tenantv1.RegisterTenantServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := userv1.RegisterUserServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := menuv1.RegisterMenuServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := permissionv1.RegisterPermissionServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := dictv1.RegisterDictTypeServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := dictv1.RegisterDictEntryServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := filev1.RegisterFileServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := auditv1.RegisterAuditServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	// Wave 1.7 补齐：组织架构网关转码面（复用同一 gRPC 拦截器链）。
	if err := identityv1.RegisterOrgUnitServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := identityv1.RegisterPositionServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	return mux, nil
}
