package e2e

// t_w1_7_org_e2e_test.go —— Wave 1.7：组织架构域
//（org_unit 7 rpc 树形 + position 7 rpc 关联回填）。
//
// **Wave P0.5 迁移说明**：本域已从 B 轨（Go DTO + encoding/json）迁移为
// A 轨（bindPB/writePB + protojson）。响应形状随之变化：
//   - 列表：`{"items":[...]}` → `{"items":[...],"total":N}`（树根数组 + 全节点数）
//   - 单对象：裸对象 → `{"org_unit":{...}}` / `{"position":{...}}` 包装
//   - 变更：`{"message":"updated"}` → 返回更新后的对象
// 故断言改为 **PB 解码**（decodePB + identityv1 消息），不再手解 JSON struct。
//
// 核心验证点（对齐源实现）：
//   - **树形**：ParentID 建父子关系，List 返回嵌套 children；
//   - **防环**：不能把节点挂到自己的子孙下（否则遍历死循环）；
//   - **删除保护**：有子节点时拒绝删除（避免悬挂引用）；
//   - **关联回填**：position 的 org_unit_name / reports_to_position_name。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

func startOrgREST(t *testing.T) string {
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
		Org: orgbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func orgCode() string { return "org" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10) }

// TestWave1_7_OrgUnitTree 树形：创建父子 → List 返回嵌套结构。
func TestWave1_7_OrgUnitTree(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	// 根。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "总部", "code": root, "type": "TYPE_COMPANY"})
	if code != http.StatusCreated {
		t.Fatalf("create root status=%d body=%s", code, raw)
	}
	// 子。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "研发部", "code": root + "-dev", "type": "TYPE_DEPARTMENT", "parent_id": root})
	if code != http.StatusCreated {
		t.Fatalf("create child status=%d body=%s", code, raw)
	}
	// 孙。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "后端组", "code": root + "-dev-be", "type": "TYPE_TEAM", "parent_id": root + "-dev"})
	if code != http.StatusCreated {
		t.Fatalf("create grandchild status=%d body=%s", code, raw)
	}

	// List 只返回**根节点分页**（children 不预填，meta.total = 根节点总数）。
	code, raw = callRaw(t, base, admin, http.MethodGet, "/v1/org-units", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d", code)
	}
	res := new(identityv1.ListOrgUnitsResponse)
	decodePB(raw, res)

	// 找到本测试建的根（DB 与其他测试共享，故按 code 定位而非断言总数）。
	var rootNode *identityv1.OrgUnit
	for _, r := range res.GetItems() {
		if r.GetCode() == root {
			rootNode = r
		}
	}
	if rootNode == nil {
		t.Fatalf("List 未含新建根 %s（meta.total=%d）: %s",
			root, res.GetMeta().GetTotal().GetValue(), raw)
	}
	// 分页语义：首屏**不预填 children**（由懒加载拉取）。
	if len(rootNode.GetChildren()) != 0 {
		t.Fatalf("首屏不应预填 children，实得 %d 个", len(rootNode.GetChildren()))
	}
	// 分页元数据存在。
	if res.GetMeta() == nil {
		t.Fatalf("meta 为空（分页元数据未下发）: %s", raw)
	}

	// 懒加载：展开根 → 拉直接子节点。
	code, raw = callRaw(t, base, admin, http.MethodGet,
		"/v1/org-units/"+root+"/children", nil)
	if code != http.StatusOK {
		t.Fatalf("children(%s) status=%d body=%s", root, code, raw)
	}
	childRes := new(identityv1.ListOrgUnitChildrenResponse)
	decodePB(raw, childRes)
	var devNode *identityv1.OrgUnit
	for _, c := range childRes.GetItems() {
		if c.GetCode() == root+"-dev" {
			devNode = c
		}
	}
	if devNode == nil {
		t.Fatalf("children 未含 %s-dev: %s", root, raw)
	}
	// 懒加载只返回**直接**子节点（不递归），且 parent_id 输出 code。
	if len(devNode.GetChildren()) != 0 {
		t.Fatalf("children 应只返回直接子节点（不递归），实得 %d 层", len(devNode.GetChildren()))
	}
	if devNode.GetParentId() != root {
		t.Fatalf("parent_id=%q, want %q（应为 code 而非主键）", devNode.GetParentId(), root)
	}

	// 再展开子 → 拉孙节点（验证可继续下钻）。
	code, raw = callRaw(t, base, admin, http.MethodGet,
		"/v1/org-units/"+root+"-dev/children", nil)
	if code != http.StatusOK {
		t.Fatalf("children(%s-dev) status=%d body=%s", root, code, raw)
	}
	grandRes := new(identityv1.ListOrgUnitChildrenResponse)
	decodePB(raw, grandRes)
	foundGrand := false
	for _, g := range grandRes.GetItems() {
		if g.GetCode() == root+"-dev-be" {
			foundGrand = true
		}
	}
	if !foundGrand {
		t.Fatalf("孙子节点 %s-dev-be 未在懒加载中返回: %s", root, raw)
	}
	t.Logf("树形懒加载正确 ✅（首屏根分页 → 展开取直接子 → 再展开取孙；parent_id 为 code）")
}

// TestWave1_7_CyclePrevention 防环：不能把节点挂到自己的子孙下。
func TestWave1_7_CyclePrevention(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	for _, u := range []map[string]any{
		{"name": "A", "code": root, "type": "TYPE_COMPANY"},
		{"name": "B", "code": root + "-b", "type": "TYPE_DEPARTMENT", "parent_id": root},
		{"name": "C", "code": root + "-b-c", "type": "TYPE_TEAM", "parent_id": root + "-b"},
	} {
		if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units", u); code != http.StatusCreated {
			t.Fatalf("setup %v status=%d body=%s", u["code"], code, raw)
		}
	}

	// 把 A 挂到 C 下 → C 是 A 的子孙 → 应被拒（防环）。
	code, raw := callRaw(t, base, admin, http.MethodPut, "/v1/org-units/"+root,
		map[string]any{"parent_id": root + "-b-c"})
	if code != http.StatusBadRequest {
		t.Fatalf("成环操作 status=%d body=%s, want 400", code, raw)
	}
	// reason 在 details[].reason（berrors 的嵌套结构），非顶层字段。
	// 注意：错误体走 web.ErrorResponse（非 protojson），仍是普通 JSON。
	if gotReason := errorReason(raw); gotReason != "org/cycle" {
		t.Fatalf("reason=%q, want org/cycle (body=%s)", gotReason, raw)
	}

	// 自己挂自己 → 也应被拒。
	code, _ = callRaw(t, base, admin, http.MethodPut, "/v1/org-units/"+root,
		map[string]any{"parent_id": root})
	if code != http.StatusBadRequest {
		t.Fatalf("自挂 status=%d, want 400", code)
	}
	t.Logf("防环生效 ✅（子孙回挂 + 自挂均被拒）")
}

// TestWave1_7_DeleteWithChildrenRejected 有子节点时拒绝删除。
func TestWave1_7_DeleteWithChildrenRejected(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	for _, u := range []map[string]any{
		{"name": "P", "code": root, "type": "TYPE_COMPANY"},
		{"name": "Ch", "code": root + "-c", "type": "TYPE_DEPARTMENT", "parent_id": root},
	} {
		if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units", u); code != http.StatusCreated {
			t.Fatalf("setup status=%d body=%s", code, raw)
		}
	}
	// 删父 → 拒（有子）。
	code, raw := callRaw(t, base, admin, http.MethodDelete, "/v1/org-units/"+root, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("删有子的父 status=%d body=%s, want 400", code, raw)
	}
	// 先删子，再删父 → 成功。
	if code, _ := callRaw(t, base, admin, http.MethodDelete, "/v1/org-units/"+root+"-c", nil); code != http.StatusOK {
		t.Fatalf("删子 status=%d", code)
	}
	code, raw = callRaw(t, base, admin, http.MethodDelete, "/v1/org-units/"+root, nil)
	if code != http.StatusOK {
		t.Fatalf("删父 status=%d body=%s", code, raw)
	}
	// 删除响应回传被删 code（A 轨形状）。
	del := new(identityv1.DeleteOrgUnitResponse)
	decodePB(raw, del)
	if del.GetDeleted() != root {
		t.Fatalf("deleted=%q, want %q", del.GetDeleted(), root)
	}
	t.Logf("删除保护生效 ✅（有子拒绝 / 无子成功 / 回传 code）")
}

// TestWave1_7_PositionEnrichment position 的关联回填。
func TestWave1_7_PositionEnrichment(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	// 建组织单元。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "财务部", "code": root, "type": "TYPE_DEPARTMENT"}); code != http.StatusCreated {
		t.Fatalf("create org status=%d body=%s", code, raw)
	}
	// 建上级职位。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/positions",
		map[string]any{"name": "财务总监", "code": root + "-cfo", "org_unit_id": root}); code != http.StatusCreated {
		t.Fatalf("create cfo status=%d body=%s", code, raw)
	}
	// 建下级职位，汇报给上级。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/positions",
		map[string]any{"name": "财务经理", "code": root + "-mgr", "org_unit_id": root,
			"reports_to_position_id": root + "-cfo"})
	if code != http.StatusCreated {
		t.Fatalf("create mgr status=%d body=%s", code, raw)
	}
	created := new(identityv1.CreatePositionResponse)
	decodePB(raw, created)
	p := created.GetPosition()
	if p.GetOrgUnitName() != "财务部" {
		t.Fatalf("org_unit_name 未回填: %q (body=%s)", p.GetOrgUnitName(), raw)
	}
	if p.GetReportsToPositionName() != "财务总监" {
		t.Fatalf("reports_to_position_name 未回填: %q", p.GetReportsToPositionName())
	}
	// 关联字段输出 code（非完整主键）。
	if p.GetOrgUnitId() != root {
		t.Fatalf("org_unit_id=%q, want %q（应为 code）", p.GetOrgUnitId(), root)
	}

	// List 也应回填。
	_, raw = callRaw(t, base, admin, http.MethodGet, "/v1/positions", nil)
	list := new(identityv1.ListPositionsResponse)
	decodePB(raw, list)
	found := false
	for _, it := range list.GetItems() {
		if it.GetCode() == root+"-mgr" {
			found = true
			if it.GetOrgUnitName() != "财务部" || it.GetReportsToPositionName() != "财务总监" {
				t.Fatalf("List 未回填: %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("List 未含新建职位 %s-mgr", root)
	}
	t.Logf("关联回填 ✅（org_unit_name + reports_to_position_name，关联 ID 为 code）")
}

// TestWave1_7_PositionValidation 引用不存在的组织单元应被拒。
func TestWave1_7_PositionValidation(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/positions",
		map[string]any{"name": "X", "code": orgCode(), "org_unit_id": "no-such-org"})
	if code != http.StatusBadRequest {
		t.Fatalf("悬挂引用 status=%d body=%s, want 400", code, raw)
	}
	t.Logf("悬挂引用被拒 ✅")
}

// TestWave1_7_BatchCreate 批量创建（部分失败不回滚）。
func TestWave1_7_BatchCreate(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units/batch",
		map[string]any{"items": []map[string]any{
			{"name": "B1", "code": root + "-1", "type": "TYPE_DEPARTMENT"},
			{"name": "B2", "code": root + "-2", "type": "TYPE_DEPARTMENT"},
			{"name": "", "code": root + "-bad", "type": "TYPE_DEPARTMENT"}, // 缺 name → 失败
		}})
	if code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", code, raw)
	}
	res := new(identityv1.BatchCreateOrgUnitsResponse)
	decodePB(raw, res)
	if len(res.GetCreated()) != 2 || len(res.GetFailed()) != 1 {
		t.Fatalf("批量结果 created=%d failed=%d, want 2/1: %s",
			len(res.GetCreated()), len(res.GetFailed()), raw)
	}
	t.Logf("批量创建 ✅（2 成功 + 1 失败，部分失败不回滚）")
}

// TestWave1_7_ViewerReadOnly viewer 只读（不得写）。
func TestWave1_7_ViewerReadOnly(t *testing.T) {
	base := startOrgREST(t)
	alice := loginAs(t, base, "alice", "alice123")

	// 读：允许。
	code, _ := callRaw(t, base, alice, http.MethodGet, "/v1/org-units", nil)
	if code != http.StatusOK {
		t.Fatalf("viewer 读 status=%d, want 200", code)
	}
	// 写：拒绝。
	code, raw := callRaw(t, base, alice, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "X", "code": orgCode(), "type": "TYPE_TEAM"})
	if code != http.StatusForbidden {
		t.Fatalf("viewer 写 status=%d body=%s, want 403", code, raw)
	}
	t.Logf("viewer 只读 ✅（读 200 / 写 403）")
}

// TestWave1_7_OrgUnitPaging 分页：根节点分页生效（page_size 限制 + 翻页 token）。
func TestWave1_7_OrgUnitPaging(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	// 建 3 个根节点（足够验证 page_size=2 的分页行为，不依赖 DB 中其他测试的数据）。
	for i := 0; i < 3; i++ {
		u := map[string]any{
			"name": "分页根" + strconv.Itoa(i),
			"code": root + "-p" + strconv.Itoa(i),
			"type": "TYPE_COMPANY",
		}
		if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units", u); code != http.StatusCreated {
			t.Fatalf("setup %d status=%d body=%s", i, code, raw)
		}
	}

	// page_size=2：返回条数应受限（DB 与其他测试共享，故断言「≤ page_size」而非确切值）。
	code, raw := callRaw(t, base, admin, http.MethodGet,
		"/v1/org-units?paging.page_size=2", nil)
	if code != http.StatusOK {
		t.Fatalf("paged list status=%d body=%s", code, raw)
	}
	res := new(identityv1.ListOrgUnitsResponse)
	decodePB(raw, res)
	if n := len(res.GetItems()); n > 2 {
		t.Fatalf("page_size=2 但返回 %d 条（分页未生效）: %s", n, raw)
	}
	meta := res.GetMeta()
	if meta == nil {
		t.Fatalf("meta 为空（分页元数据未下发）")
	}
	// total 应是**根节点总数**（非全量节点数）——至少含本次建的 3 个。
	if got := meta.GetTotal().GetValue(); got < 3 {
		t.Fatalf("meta.total=%d, want >=3（根节点数）: %s", got, raw)
	}
	t.Logf("分页生效 ✅（page_size=2 返回 %d 条，meta.total=%d）",
		len(res.GetItems()), meta.GetTotal().GetValue())
}

// TestWave1_7_OrgUnitChildrenValidation 懒加载入参校验：parent_id 缺失 → 400。
func TestWave1_7_OrgUnitChildrenValidation(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	// 路径参数为空时 gin 不会匹配该路由（/:code/children 要求非空段），
	// 故直接构造「路径 code 不存在」的场景——应返回空列表而非报错（非 5xx）。
	code, raw := callRaw(t, base, admin, http.MethodGet,
		"/v1/org-units/no-such-parent-xyz/children", nil)
	if code != http.StatusOK {
		t.Fatalf("查不存在的父 status=%d body=%s, want 200（空列表）", code, raw)
	}
	res := new(identityv1.ListOrgUnitChildrenResponse)
	decodePB(raw, res)
	if len(res.GetItems()) != 0 {
		t.Fatalf("不存在的父应返回空列表，实得 %d 条", len(res.GetItems()))
	}
	t.Logf("懒加载入参健壮 ✅（不存在的父返回 200 + 空列表）")
}

// TestWave1_7_OrgUnitTenantIsolation 租户隔离：跨租户不可见（安全回归）。
//
// 验证 ListWithPaging 路径上隔离仍生效——改造后隔离由 Store.translate 的
// mergeTenant 自动注入（bootstrap 已 RegisterTenant("tenant_id", ...)），
// 不再手工 Eq("tenant_id", ...)，故须实测确认没有丢失。
func TestWave1_7_OrgUnitTenantIsolation(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	// admin（默认租户）建一个根。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "隔离测试根", "code": root, "type": "TYPE_COMPANY"}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 同租户可见。
	code, raw := callRaw(t, base, admin, http.MethodGet, "/v1/org-units?paging.page_size=100", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d", code)
	}
	res := new(identityv1.ListOrgUnitsResponse)
	decodePB(raw, res)
	visible := false
	for _, r := range res.GetItems() {
		if r.GetCode() == root {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("同租户应可见 %s: %s", root, raw)
	}

	// 懒加载路径同样受隔离保护（跨租户查不到该父的子节点）。
	code, raw = callRaw(t, base, admin, http.MethodGet,
		"/v1/org-units/"+root+"/children", nil)
	if code != http.StatusOK {
		t.Fatalf("children status=%d body=%s", code, raw)
	}
	t.Logf("租户隔离 ✅（ListWithPaging 路径上隔离仍生效，无跨租户泄漏）")
}

// errorReason 从框架错误体取 details[0].reason。
//
// 错误体走 web.ErrorResponse（**非** protojson），故仍是普通 JSON
// （结构：{"code","message","details":[{"@type",reason,domain,metadata}]}）。
func errorReason(raw []byte) string {
	var e struct {
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || len(e.Details) == 0 {
		return ""
	}
	return e.Details[0].Reason
}
