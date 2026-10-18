// Package pland 是 plan 域的装配声明包（批 4 第三波，机械域）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
//
// 特殊点：无 proto 纯 gin 面。
package pland

import (
	"context"
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	planbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/plan"
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
	// PlanStore Plan 仓储（InitBridges 的 repos.Plan 同一实例，无双构造）。
	PlanStore func() *store.Store[authmodel.Plan]
	// ModuleStore PlanModule 仓储（InitBridges 的 repos.PlanModule 同一实例，无双构造）。
	ModuleStore func() *store.Store[authmodel.PlanModule]
	// QuotaStore PlanQuota 仓储（InitBridges 的 repos.PlanQuota 同一实例，无双构造）。
	QuotaStore func() *store.Store[authmodel.PlanQuota]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 plan 域的装配声明值。Routes 委托 hgin.RegisterPlan——传输适配
// 不搬家，路由面/中间件链与迁移前逐行等价；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *planbiz.Biz
	return appkit.Domain{
		Name: "plan",
		Wire: func(_ context.Context, _ *appkit.App) error {
		ps := d.PlanStore()
		ms := d.ModuleStore()
		qs := d.QuotaStore()
			if ps == nil || ms == nil || qs == nil {
				return fmt.Errorf("pland: store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = planbiz.New(ps, ms, qs)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("pland: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterPlan(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
