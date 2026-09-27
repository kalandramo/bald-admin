// Package apiserver 是 bald-admin 的 HTTP 服务装配根（server 层）。
//
// 只负责把 handler 挂到 *gin.Engine；认证/授权依赖由 bootstrap 包注入（M1）。
// 不在此写业务，也不在此直接依赖 bald-authn-jwt（保持 server 层与桥接解耦）。
package apiserver

import (
	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"

	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	"github.com/kalandramo/bald-admin/internal/apiserver/middleware/apiaudit"
)

// RegisterRoutes 把本应用所有路由挂到 e。认证/授权依赖由装配层注入
//（Wave 4.2：真实实例直传，不再经 lazy 请求期适配器——装配已移到运行期，
// InitBridges 之后，故 Authenticator/Authorizer/TokenStore 均就绪）；业务对象经
// BizSet 聚合传入（wire 装配，见 bizset.go）。
//
// authenticator 应带**吊销检查**（token.NewRevocationChecker）——登出拉黑的 token
// 在中间件层即被拒绝，与 ValidateToken 语义同源。
func RegisterRoutes(e *gingonic.Engine, authenticator authn.Authenticator,
	authorizer authz.Authorizer, biz *BizSet) {
	RegisterRoutesWithAuth(e, authenticator, authorizer, biz)
}

// RegisterRoutesWithAuth 与 RegisterRoutes 相同（保留为显式注入入口，供 e2e 传
// 自定义认证器/授权器）。
func RegisterRoutesWithAuth(e *gingonic.Engine, authenticator authn.Authenticator,
	authorizer authz.Authorizer, biz *BizSet) {
	// Wave 5.1：api 类审计中间件（源 ApiAuditLog）——记录协议层信息
	// （method/path/status/latency），与框架 operation 类审计正交。
	// 挂在此处（而非 main.go 的 router）保证**生产与 e2e 共用同一装配路径**。
	e.Use(apiaudit.Middleware())

	hgin.RegisterHealth(e)
	hgin.RegisterOpenAPI(e)
	hgin.RegisterAuth(e, authenticator, authorizer, biz.Auth, biz.Secret)
	hgin.RegisterTenant(e, authenticator, authorizer, biz.Tenant)
	hgin.RegisterUser(e, authenticator, authorizer, biz.User)
	hgin.RegisterMenu(e, authenticator, authorizer, biz.Menu)
	hgin.RegisterPermission(e, authenticator, authorizer, biz.Permission)
	hgin.RegisterDict(e, authenticator, authorizer, biz.Dict)
	hgin.RegisterFile(e, authenticator, authorizer, biz.File)
	hgin.RegisterAudit(e, authenticator, authorizer, biz.AuditLog)
	if biz.MFA != nil {
		hgin.RegisterMFA(e, authenticator, authorizer, biz.MFA)
	}
	if biz.Identity != nil {
		hgin.RegisterIdentity(e, authenticator, authorizer, biz.Identity)
	}
	if biz.Org != nil {
		hgin.RegisterOrg(e, authenticator, authorizer, biz.Org)
	}
	if biz.Task != nil {
		hgin.RegisterTask(e, authenticator, authorizer, biz.Task)
	}
	if biz.Message != nil {
		hgin.RegisterMessage(e, authenticator, authorizer, biz.Message)
	}
	if biz.Dashboard != nil {
		hgin.RegisterDashboard(e, authenticator, authorizer, biz.Dashboard)
	}
	if biz.PermGroup != nil {
		hgin.RegisterPermGroup(e, authenticator, authorizer, biz.PermGroup)
	}
	if biz.Plan != nil {
		hgin.RegisterPlan(e, authenticator, authorizer, biz.Plan)
	}
	if biz.Language != nil {
		hgin.RegisterLanguage(e, authenticator, authorizer, biz.Language)
	}
	if biz.Portal != nil {
		hgin.RegisterAdminPortal(e, authenticator, authorizer, biz.Portal)
	}
	if biz.CacheMonitor != nil {
		hgin.RegisterRedisCacheMonitor(e, authenticator, authorizer, biz.CacheMonitor)
	}
}

// ComponentFactory 是管理面组件工厂的包级别名（re-export，供 cmd 层构造工厂目录）。
type ComponentFactory = hgin.ComponentFactory

// RegisterAdmin 挂载管理面路由（M10.2，re-export）。
func RegisterAdmin(e *gingonic.Engine, appFn func() *appkit.AppKit,
	authenticator authn.Authenticator, authorizer authz.Authorizer,
	factories map[string]ComponentFactory) {
	hgin.RegisterAdmin(e, appFn, authenticator, authorizer, factories)
}
