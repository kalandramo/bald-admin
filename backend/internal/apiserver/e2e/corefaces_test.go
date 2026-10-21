package e2e

// corefaces_test.go —— handler 级 e2e 直构接线位的**域路径形态**。
//
// 本 helper 承接共用「核心九域」的 e2e：挂载共面（apiserver.RegisterBaseFaces
// ——apiaudit 中间件 + service info + openapi）后**逐域**调用
// handler/gin 的 Register*（与各域声明包 Routes 委托的同一函数），biz 仍由
// 本包**当场直构**——每次调用全新实例，测试间隔离价值原样保留（不是共享夹具）。
//
// 路由注册为逐域显式调用（与生产域路径同构）；变体轴（自定义认证器/认证 biz
// 装饰器/dict 缓存/file 存储轴）经参数注入，零值走默认直构。各自文件的
// start*REST helper 在调用前后仍可追加域专属 Register*（如 RegisterMFA）与
// biz setter。

import (
	"testing"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald/cache"
)

// registerCoreFaces 挂载共面 + 核心九域（auth/secret/tenant/user/menu/
// permission/dict/file/auditlog）。变体注入：routeAuthn 覆盖路由层认证器
// （吊销装饰器/memtoken 轴）；auth 覆盖 auth biz（限流/熔断/重试/平台判据/
// 令牌存储装饰等先构造后传入）；dictCache 接 dict biz 的 Cache-Aside；
// file 覆盖 file biz（minio 直连轴）。nil 参数 = 默认直构。
func registerCoreFaces(t *testing.T, e *gingonic.Engine, routeAuthn authn.Authenticator,
	auth *authbiz.Biz, dictCache cache.Cache, file *filebiz.Biz) {
	t.Helper()
	if routeAuthn == nil {
		routeAuthn = bootstrappkg.Authenticator
	}
	if auth == nil {
		auth = authbiz.New(bootstrappkg.Signer, repos.User, repos.Tenant, bootstrappkg.ReloadPolicies)
	}
	if file == nil {
		file = filebiz.New(repos.File, nil, "")
	}
	authzr := bootstrappkg.Authorizer
	apiserver.RegisterBaseFaces(e)
	hgin.RegisterAuth(e, routeAuthn, authzr, auth)
	hgin.RegisterSecret(e, routeAuthn, authzr, secretbiz.New(repos.Secret, nil))
	hgin.RegisterTenant(e, routeAuthn, authzr, tenantbiz.New(repos.Tenant))
	hgin.RegisterUser(e, routeAuthn, authzr, userbiz.New(repos.User))
	hgin.RegisterMenu(e, routeAuthn, authzr, menubiz.New(repos.Menu))
	hgin.RegisterPermission(e, routeAuthn, authzr, permissionbiz.New(repos.Permission, repos.RolePolicy))
	hgin.RegisterDict(e, routeAuthn, authzr, dictbiz.New(repos.DictType, repos.DictEntry, dictCache))
	hgin.RegisterFile(e, routeAuthn, authzr, file)
	hgin.RegisterAudit(e, routeAuthn, authzr, auditlogbiz.New(repos.Audit))
}
