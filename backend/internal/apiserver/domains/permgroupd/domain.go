// Package permgroupd 是 permgroup 域的装配声明包（批 4 第三波，机械域）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
//
// 特殊点：无 proto 纯 gin 面。
package permgroupd

import (
	"context"
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
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

// Domain 返回 permgroup 域的装配声明值。Routes 委托 hgin.RegisterPermGroup——传输适配
// 不搬家，路由面/中间件链与迁移前逐行等价；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *pgbiz.Biz
	return appkit.Domain{
		Name: "permgroup",
		Wire: func(_ context.Context, _ *appkit.App) error {
		gs := d.GroupStore()
		els := d.EvalLogStore()
			if gs == nil || els == nil {
				return fmt.Errorf("permgroupd: store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = pgbiz.New(gs, els)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("permgroupd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterPermGroup(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
