package e2e

// t_w6_2_admin_portal_e2e_test.go —— Wave 6.2：管理面真新增（AdminPortalService）。
//
// ## 源的 3 rpc（admin/service/v1/i_admin_portal.proto）
//
//   - GetNavigation        GET /admin/v1/routes          前端路由表（菜单树，去 BUTTON）
//   - GetMyPermissionCode  GET /admin/v1/perm-codes       当前用户权限码列表
//   - GetInitialContext    GET /admin/v1/initial-context   菜单树 + 权限码（一次拉全）
//
// ## 聚合链（源 admin_portal_service.go 实测）
//
//   User.Roles(角色名) → Role.Perms(权限码) → Permission.MenuIDs(菜单ID) → Menu 树
//
// 即「当前登录用户 → 其角色 → 角色权限码 → 权限点关联的菜单 → 过滤出路由」。
// 这是**跨域聚合**（user + role + permission + menu 四域），无单一对标 biz
// ——故是 Wave 6 的真新增（裁定见计划 §Wave 6.1）。
//
// ## 测试数据策略（关键）
//
// seed 的 admin 角色 Perms 只有 3 个权限码（secret:get/secret:delete/auth:get），
// 其中唯 secret:delete 关联菜单且是 BUTTON 类型（会被路由过滤掉）——故用 seed
// 数据断言 GetNavigation 会得到接近空的树。本测试**自造可控数据**（新角色 +
// 关联菜单的非按钮权限码）来验证聚合链真正工作，不依赖 seed 巧合。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/admin/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	portal "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/portal"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// startPortalREST 起真实 gin 引擎 + 真实 SQLite（portal biz 注入）。
func startPortalREST(t *testing.T) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		Portal: portal.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestWave6_2_GetMyPermissionCode 当前用户权限码（聚合 User→Role→Perms）。
func TestWave6_2_GetMyPermissionCode(t *testing.T) {
	base := startPortalREST(t)
	// u-admin 的 seed 角色是 admin，Perms 含 secret:get/secret:delete/auth:get。
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/admin/v1/perm-codes", nil)
	if code != http.StatusOK {
		t.Fatalf("perm-codes status=%d body=%s", code, raw)
	}
	out := new(adminv1.ListPermissionCodeResponse)
	decodePB(raw, out)
	got := map[string]bool{}
	for _, c := range out.GetCodes() {
		got[c] = true
	}
	// admin 角色 Perms 里的权限码应全部返回。
	for _, want := range []string{"secret:get", "secret:delete", "auth:get"} {
		if !got[want] {
			t.Fatalf("权限码缺 %q，实际: %v", want, out.GetCodes())
		}
	}
	t.Logf("权限码聚合正确: %v", out.GetCodes())
}

// TestWave6_2_GetNavigation 导航路由（聚合 User→Role→Perms→Permission.MenuIDs→Menu 树）。
//
// 自造可控数据：新角色 + 关联「非按钮菜单」的权限码，验证聚合链。
func TestWave6_2_GetNavigation(t *testing.T) {
	base := startPortalREST(t)
	ctx := context.Background()

	// 造数据：角色 r-nav 的 Perms 含 nav:list；权限点 nav:list 关联 menu-system + menu-tenant。
	// （直接写 store 保证测试隔离；store 是包级桥接，InitBridges 后可用。）
	if err := bootstrappkg.RoleStore.Create(ctx, &authmodel.Role{ID: "r-nav", Perms: "nav:list"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := bootstrappkg.PermissionStore.Create(ctx, &authmodel.Permission{
		ID: "nav:list", Name: "导航测试权限", MenuIDs: "menu-system,menu-tenant",
	}); err != nil {
		t.Fatalf("create permission: %v", err)
	}
	// 授权：r-nav 需能访问 /admin/v1/routes——该组用**自定义 object
	// "admin_portal"**（见 handler 注释：避免与 /admin/components 的 "admin" 撞名）。
	// 业务聚合与授权是正交关注点，故测试显式补策略行（否则 403，与聚合无关）。
	if err := bootstrappkg.RolePolicyStore.Create(ctx, &authmodel.RolePolicy{
		ID: "r-nav:admin_portal:get", Role: "r-nav", Object: "admin_portal", Action: "get",
	}); err != nil {
		t.Fatalf("create role policy: %v", err)
	}
	// **关键**：casbin 的 g 行（用户→角色）来自 User 表（loadPolicyCSV 实测）——
	// 必须建真实用户 u-nav（Roles=r-nav），否则 subject=u-nav 无角色 → 403。
	if err := bootstrappkg.UserStore.Create(ctx, &authmodel.User{
		ID: "u-nav", Username: "navuser", PasswordHash: "x", TenantID: "t-default", Roles: "r-nav",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	// 策略数据化装载后需热重载才生效（D7 的绕行：改角色后须调 ReloadPolicies）。
	if err := bootstrappkg.ReloadPolicies(); err != nil {
		t.Fatalf("reload policies: %v", err)
	}

	// 用自造角色签发 token（Roles = r-nav）。
	tok := tenantToken(t, "navuser", "u-nav", "r-nav", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/admin/v1/routes", nil)
	if code != http.StatusOK {
		t.Fatalf("routes status=%d body=%s", code, raw)
	}
	out := new(adminv1.ListRouteResponse)
	decodePB(raw, out)
	if len(out.GetItems()) == 0 {
		t.Fatalf("导航为空——聚合链未生效（role r-nav → perm nav:list → menu-system/menu-tenant）")
	}
	// 断言返回的是菜单树（含子节点 menu-tenant 挂在 menu-system 下）。
	found := false
	for _, it := range out.GetItems() {
		if it.GetPath() == "/system" {
			found = true
			if len(it.GetChildren()) == 0 {
				t.Fatalf("menu-system 应有子菜单（menu-tenant）")
			}
		}
		// BUTTON 类型不得出现在路由里。
		if it.GetPath() == "secret:delete" {
			t.Fatalf("BUTTON 类型不应进路由: %+v", it)
		}
	}
	if !found {
		t.Fatalf("导航缺 menu-system: %+v", out.GetItems())
	}
	t.Logf("导航聚合正确: %d 个顶级路由", len(out.GetItems()))
}

// TestWave6_2_GetInitialContext 初始上下文（菜单树 + 权限码一次拉全）。
func TestWave6_2_GetInitialContext(t *testing.T) {
	base := startPortalREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/admin/v1/initial-context", nil)
	if code != http.StatusOK {
		t.Fatalf("initial-context status=%d body=%s", code, raw)
	}
	out := new(adminv1.InitialContextResponse)
	decodePB(raw, out)
	// 权限码非空（admin 角色有 3 个）。
	if len(out.GetPermissions()) == 0 {
		t.Fatalf("initial-context 权限码为空")
	}
	got := map[string]bool{}
	for _, c := range out.GetPermissions() {
		got[c] = true
	}
	if !got["auth:get"] {
		t.Fatalf("initial-context 应含 auth:get，实际 %v", out.GetPermissions())
	}
	// menus 字段存在（可为空——seed 的 admin 权限码无关联非按钮菜单）。
	t.Logf("初始上下文正确: %d 权限码 / %d 菜单", len(out.GetPermissions()), len(out.GetMenus()))
}
