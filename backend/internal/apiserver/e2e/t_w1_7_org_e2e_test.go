package e2e

// t_w1_7_org_e2e_test.go —— Wave 1.7：组织架构域
//（org_unit 7 rpc 树形 + position 7 rpc 关联回填）。
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
		map[string]any{"name": "总部", "code": root, "type": "COMPANY"})
	if code != http.StatusCreated {
		t.Fatalf("create root status=%d body=%s", code, raw)
	}
	// 子。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "研发部", "code": root + "-dev", "type": "DEPARTMENT", "parent_id": root})
	if code != http.StatusCreated {
		t.Fatalf("create child status=%d body=%s", code, raw)
	}
	// 孙。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "后端组", "code": root + "-dev-be", "type": "TEAM", "parent_id": root + "-dev"})
	if code != http.StatusCreated {
		t.Fatalf("create grandchild status=%d body=%s", code, raw)
	}

	// List 应返回树形（根含 children，children 含 children）。
	code, raw = callRaw(t, base, admin, http.MethodGet, "/v1/org-units", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d", code)
	}
	var res struct {
		Items []struct {
			Code     string `json:"code"`
			Children []struct {
				Code     string `json:"code"`
				Children []struct {
					Code string `json:"code"`
				} `json:"children"`
			} `json:"children"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	foundDepth := 0
	for _, r := range res.Items {
		if r.Code != root {
			continue
		}
		foundDepth = 1
		for _, c := range r.Children {
			if c.Code == root+"-dev" {
				foundDepth = 2
				for _, g := range c.Children {
					if g.Code == root+"-dev-be" {
						foundDepth = 3
					}
				}
			}
		}
	}
	if foundDepth != 3 {
		t.Fatalf("树形深度=%d, want 3（根→子→孙）: %s", foundDepth, raw)
	}
	t.Logf("树形结构正确 ✅（3 层嵌套）")
}

// TestWave1_7_CyclePrevention 防环：不能把节点挂到自己的子孙下。
func TestWave1_7_CyclePrevention(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	for _, u := range []map[string]any{
		{"name": "A", "code": root, "type": "COMPANY"},
		{"name": "B", "code": root + "-b", "type": "DEPARTMENT", "parent_id": root},
		{"name": "C", "code": root + "-b-c", "type": "TEAM", "parent_id": root + "-b"},
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
	var e struct {
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	_ = json.Unmarshal(raw, &e)
	gotReason := ""
	if len(e.Details) > 0 {
		gotReason = e.Details[0].Reason
	}
	if gotReason != "org/cycle" {
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
		{"name": "P", "code": root, "type": "COMPANY"},
		{"name": "Ch", "code": root + "-c", "type": "DEPARTMENT", "parent_id": root},
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
	if code, raw := callRaw(t, base, admin, http.MethodDelete, "/v1/org-units/"+root, nil); code != http.StatusOK {
		t.Fatalf("删父 status=%d body=%s", code, raw)
	}
	t.Logf("删除保护生效 ✅（有子拒绝 / 无子成功）")
}

// TestWave1_7_PositionEnrichment position 的关联回填。
func TestWave1_7_PositionEnrichment(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	root := orgCode()

	// 建组织单元。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "财务部", "code": root, "type": "DEPARTMENT"}); code != http.StatusCreated {
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
	var p struct {
		OrgUnitName           string `json:"org_unit_name"`
		ReportsToPositionName string `json:"reports_to_position_name"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.OrgUnitName != "财务部" {
		t.Fatalf("org_unit_name 未回填: %q (body=%s)", p.OrgUnitName, raw)
	}
	if p.ReportsToPositionName != "财务总监" {
		t.Fatalf("reports_to_position_name 未回填: %q", p.ReportsToPositionName)
	}

	// List 也应回填。
	_, raw = callRaw(t, base, admin, http.MethodGet, "/v1/positions", nil)
	var list struct {
		Items []struct {
			Code                  string `json:"code"`
			OrgUnitName           string `json:"org_unit_name"`
			ReportsToPositionName string `json:"reports_to_position_name"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &list)
	for _, it := range list.Items {
		if it.Code == root+"-mgr" {
			if it.OrgUnitName != "财务部" || it.ReportsToPositionName != "财务总监" {
				t.Fatalf("List 未回填: %+v", it)
			}
		}
	}
	t.Logf("关联回填 ✅（org_unit_name + reports_to_position_name）")
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
			{"name": "B1", "code": root + "-1", "type": "DEPARTMENT"},
			{"name": "B2", "code": root + "-2", "type": "DEPARTMENT"},
			{"name": "", "code": root + "-bad", "type": "DEPARTMENT"}, // 缺 name → 失败
		}})
	if code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", code, raw)
	}
	var res struct {
		Created []any    `json:"created"`
		Failed  []string `json:"failed"`
	}
	_ = json.Unmarshal(raw, &res)
	if len(res.Created) != 2 || len(res.Failed) != 1 {
		t.Fatalf("批量结果 created=%d failed=%d, want 2/1: %s", len(res.Created), len(res.Failed), raw)
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
		map[string]any{"name": "X", "code": orgCode(), "type": "TEAM"})
	if code != http.StatusForbidden {
		t.Fatalf("viewer 写 status=%d body=%s, want 403", code, raw)
	}
	t.Logf("viewer 只读 ✅（读 200 / 写 403）")
}
