// Package permgroupd 是 permgroup 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无 proto 纯 gin 面。
package permgroupd

import (
	"context"
	"net/http"

	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
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
	// GroupStore PermissionGroup 仓储（InitBridges 的 repos.PermGroup 同一实例，无双构造）。
	GroupStore func() *store.Store[authmodel.PermissionGroup]
	// EvalLogStore PolicyEvaluationLog 仓储（InitBridges 的 repos.PolicyEvalLog 同一实例，无双构造）。
	EvalLogStore func() *store.Store[authmodel.PolicyEvaluationLog]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// wired 是域 Wire 构造的 biz 端口。授权记录装饰器（bootstrap.Authorizer 最外层的
// RecordingAuthorizer）经装配层注入的迟到 getter 读取同一实例——与 taskd/messaged
// 同款（见 taskd/domain.go 的 Biz() 注），无双构造。
var wired *pgbiz.Biz

// Biz 返回 permgroup 域的 biz 端口（未 Wire 时 nil——调用方判空即降级不记录）。
func Biz() *pgbiz.Biz { return wired }

// Domain 返回 permgroup 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain）：routes 委托 hgin.RegisterPermGroup——传输适配
// 不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *pgbiz.Biz
	return domains.StandardDomain("permgroup", "permgroupd",
		func(context.Context, *appkit.App) error {
			gs, err := domains.RequireStore("permgroupd", "store", d.GroupStore())
			if err != nil {
				return err
			}
			els, err := domains.RequireStore("permgroupd", "store", d.EvalLogStore())
			if err != nil {
				return err
			}
			biz = pgbiz.New(gs, els)
			wired = biz
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("permgroupd", h)
			if err != nil {
				return err
			}
			hgin.RegisterPermGroup(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
}
