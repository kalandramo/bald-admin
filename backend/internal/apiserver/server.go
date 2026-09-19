// Package apiserver 是 go-bald-admin 的 HTTP 服务装配根（server 层）。
//
// 只负责把 handler 挂到 *gin.Engine；认证/授权依赖由 bootstrap 包注入（M1）。
// 不在此写业务，也不在此直接依赖 bald-authn-jwt（保持 server 层与桥接解耦）。
package apiserver

import (
	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"

	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// RegisterRoutes 把本应用所有路由挂到 e。认证/授权依赖从 bootstrap 注入；
// 业务对象经 BizSet 聚合传入（wire 装配，见 bizset.go）。
//
// Wave 1d：默认认证器带**吊销检查**（LazyAuthenticatorWithRevocation）——
// 登出拉黑的 token 在中间件层即被拒绝，与 ValidateToken 语义同源。
func RegisterRoutes(e *gingonic.Engine, biz *BizSet) {
	RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticatorWithRevocation(), biz)
}

// RegisterRoutesWithAuth 与 RegisterRoutes 相同，但允许注入自定义认证器。
// Wave 1d 新增：e2e 与 main 需要注入**被 token.RevocationChecker 装饰**的认证器
// （验签后查吊销名单）——装饰器是 authn.Authenticator 的合法实现，本函数让
// 装配层可替换而不侵入框架中间件。
func RegisterRoutesWithAuth(e *gingonic.Engine, authenticator authn.Authenticator, biz *BizSet) {
	hgin.RegisterHealth(e)
	hgin.RegisterOpenAPI(e)
	hgin.RegisterAuth(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Auth, biz.Secret)
	hgin.RegisterTenant(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Tenant)
	hgin.RegisterUser(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.User)
	hgin.RegisterMenu(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Menu)
	hgin.RegisterPermission(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Permission)
	hgin.RegisterDict(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Dict)
	hgin.RegisterFile(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.File)
	hgin.RegisterAudit(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.AuditLog)
	if biz.MFA != nil {
		hgin.RegisterMFA(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.MFA)
	}
	if biz.Identity != nil {
		hgin.RegisterIdentity(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Identity)
	}
	if biz.Org != nil {
		hgin.RegisterOrg(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Org)
	}
	if biz.Task != nil {
		hgin.RegisterTask(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Task)
	}
	if biz.Message != nil {
		hgin.RegisterMessage(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Message)
	}
	if biz.Dashboard != nil {
		hgin.RegisterDashboard(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Dashboard)
	}
	if biz.PermGroup != nil {
		hgin.RegisterPermGroup(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.PermGroup)
	}
	if biz.Plan != nil {
		hgin.RegisterPlan(e, authenticator, bootstrappkg.LazyAuthorizer(), biz.Plan)
	}
}

// ComponentFactory 是管理面组件工厂的包级别名（re-export，供 cmd 层构造工厂目录）。
type ComponentFactory = hgin.ComponentFactory

// RegisterAdmin 挂载管理面路由（M10.2，re-export）。
func RegisterAdmin(e *gingonic.Engine, appFn func() *appkit.AppKit, factories map[string]ComponentFactory) {
	hgin.RegisterAdmin(e, appFn, factories)
}
