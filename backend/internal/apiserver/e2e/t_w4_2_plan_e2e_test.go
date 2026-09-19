package e2e

// t_w4_2_plan_e2e_test.go —— Wave 4.2：套餐三件套（源 14 rpc）。
//
// 源：`i_plan.proto`(5) + `i_plan_module.proto`(5) + `i_plan_quota.proto`(4)。
//
// ## 核心语义：**级联删除**（本波重点）
//
// 源用 ent Edge + `OnDelete: entsql.Cascade`（`plan.go:93,100`）——
// **删套餐时 DB 自动级联删其 modules/quotas**。
//
// **与 org/permgroup 的语义差异（源的差异，非实现随意）**：
//   - org / permgroup：**有子节点时拒绝删除**（Restrict 语义）
//   - plan：**级联删除**（Cascade 语义）
//
// 本测试专门锁住 plan 的级联语义——若后人照搬 org 的「拒绝删除」写法会错。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	planbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/plan"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

func startPlanREST(t *testing.T) string {
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
		Plan: planbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func planSuffix() string { return "pl" + time.Now().Format("150405.000000") }

// TestWave4_2_CascadeDelete —— **本波最重要的验证点**。
//
// 删套餐 → 其 modules/quotas 一并删除（源 ent Cascade 语义）。
// 与 org/permgroup 的「拒绝删除」相反——这是源的差异。
func TestWave4_2_CascadeDelete(t *testing.T) {
	base := startPlanREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	biz := planbiz.New()
	ctx := context.Background()
	sfx := planSuffix()

	// 建套餐 + 2 模块 + 2 配额。
	p, err := biz.CreatePlan(ctx, "t-default", "p-"+sfx, planbiz.Plan{
		Name: "企业版", Version: "ENTERPRISE", DataRetentionDays: 90,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	for _, mod := range []string{"opm", "order"} {
		if _, err := biz.CreateModule(ctx, "t-default", p.ID, mod); err != nil {
			t.Fatalf("CreateModule(%s): %v", mod, err)
		}
	}
	if _, err := biz.CreateQuota(ctx, "t-default", p.ID, "max_users", 1000); err != nil {
		t.Fatalf("CreateQuota(max_users): %v", err)
	}
	if _, err := biz.CreateQuota(ctx, "t-default", p.ID, "max_storage", 10240); err != nil {
		t.Fatalf("CreateQuota(max_storage): %v", err)
	}

	// 删前确认子表有数据。
	mods, _ := biz.ListModules(ctx, p.ID)
	quotas, _ := biz.ListQuotas(ctx, p.ID)
	if len(mods) != 2 || len(quotas) != 2 {
		t.Fatalf("前置条件不满足: modules=%d quotas=%d", len(mods), len(quotas))
	}
	t.Logf("删前：套餐 %s 有 %d 模块 / %d 配额", p.Name, len(mods), len(quotas))

	// **删套餐（HTTP）——应级联删子表**。
	st, raw := callRaw(t, base, admin, http.MethodDelete, "/v1/plans/"+p.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("删除套餐 status=%d body=%s", st, raw)
	}

	// 1) 套餐本体没了。
	if _, err := biz.GetPlan(ctx, p.ID); err == nil {
		t.Fatal("套餐删除后仍可查到")
	}
	// 2) **子表也清了**（级联生效）。
	mods, _ = biz.ListModules(ctx, p.ID)
	quotas, _ = biz.ListQuotas(ctx, p.ID)
	if len(mods) != 0 {
		t.Fatalf("级联失败：删套餐后仍有 %d 个模块", len(mods))
	}
	if len(quotas) != 0 {
		t.Fatalf("级联失败：删套餐后仍有 %d 个配额", len(quotas))
	}
	t.Log("确认：删套餐级联清除其 modules/quotas（源 ent Cascade 语义）")
}

// TestWave4_2_PlanCRUD —— 套餐 CRUD + 校验。
func TestWave4_2_PlanCRUD(t *testing.T) {
	base := startPlanREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	sfx := planSuffix()

	// 缺 name / code → 400。
	if st, _ := callRaw(t, base, admin, http.MethodPost, "/v1/plans",
		map[string]any{"code": "x-" + sfx}); st != http.StatusBadRequest {
		t.Fatalf("缺 name status=%d, want 400", st)
	}
	if st, _ := callRaw(t, base, admin, http.MethodPost, "/v1/plans",
		map[string]any{"name": "只有名字"}); st != http.StatusBadRequest {
		t.Fatalf("缺 code status=%d, want 400", st)
	}

	// 创建。
	code := "p-" + sfx
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/plans",
		map[string]any{"code": code, "name": "专业版", "version": "PRO", "data_retention_days": 30})
	if st != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", st, raw)
	}
	var p planbiz.Plan
	_ = json.Unmarshal(raw, &p)
	if p.Status != "ON" {
		t.Fatalf("默认状态=%q, want ON", p.Status)
	}

	// 重复 → 409。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/plans",
		map[string]any{"code": code, "name": "重复"})
	if st != http.StatusConflict {
		t.Fatalf("重复 code status=%d, want 409", st)
	}

	// 更新。
	st, _ = callRaw(t, base, admin, http.MethodPut, "/v1/plans/"+p.ID,
		map[string]any{"name": "改名后", "status": "OFF", "data_retention_days": 60})
	if st != http.StatusOK {
		t.Fatalf("update status=%d", st)
	}
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/plans/"+p.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("get status=%d", st)
	}
	var got planbiz.Plan
	_ = json.Unmarshal(raw, &got)
	if got.Name != "改名后" || got.Status != "OFF" || got.DataRetentionDays != 60 {
		t.Fatalf("更新后不符: %+v", got)
	}
	t.Logf("套餐 CRUD 正确: %+v", got)

	// 列表。
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/plans", nil)
	if st != http.StatusOK {
		t.Fatalf("list status=%d", st)
	}
	var lst struct {
		Items []planbiz.Plan `json:"items"`
	}
	_ = json.Unmarshal(raw, &lst)
	found := false
	for _, x := range lst.Items {
		if x.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("列表中未找到刚创建的套餐")
	}

	// 不存在的 → 404。
	if st, _ := callRaw(t, base, admin, http.MethodGet, "/v1/plans/no-such", nil); st != http.StatusNotFound {
		t.Fatalf("不存在 status=%d, want 404", st)
	}

	_, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/plans/"+p.ID, nil)
}

// TestWave4_2_ModuleAndQuota —— 模块/配额的增删改查 + 父校验。
func TestWave4_2_ModuleAndQuota(t *testing.T) {
	base := startPlanREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	biz := planbiz.New()
	ctx := context.Background()
	sfx := planSuffix()

	p, err := biz.CreatePlan(ctx, "t-default", "mq-"+sfx, planbiz.Plan{Name: "基础版"})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	defer func() { _ = biz.DeletePlan(ctx, p.ID) }()

	// 父不存在 → 400（避免悬挂引用）。
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/plan-modules",
		map[string]any{"plan_id": "no-such-plan", "module": "opm"})
	if st != http.StatusBadRequest {
		t.Fatalf("父不存在应 400，实际 %d body=%s", st, raw)
	}
	t.Log("确认：父套餐不存在时拒绝加模块")

	// 正常加模块。
	st, raw = callRaw(t, base, admin, http.MethodPost, "/v1/plan-modules",
		map[string]any{"plan_id": p.ID, "module": "opm"})
	if st != http.StatusCreated {
		t.Fatalf("加模块 status=%d body=%s", st, raw)
	}
	var mod planbiz.Module
	_ = json.Unmarshal(raw, &mod)

	// 同套餐同模块重复 → 409（主键含 plan+module，天然幂等约束）。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/plan-modules",
		map[string]any{"plan_id": p.ID, "module": "opm"})
	if st != http.StatusConflict {
		t.Fatalf("重复模块 status=%d, want 409", st)
	}
	t.Log("确认：同套餐同模块重复添加返回 409")

	// 更新模块标识。
	st, _ = callRaw(t, base, admin, http.MethodPut, "/v1/plan-modules/"+mod.ID,
		map[string]any{"module": "order"})
	if st != http.StatusOK {
		t.Fatalf("更新模块 status=%d", st)
	}
	got, _ := biz.GetModule(ctx, mod.ID)
	if got.Module != "order" {
		t.Fatalf("模块更新后=%q, want order", got.Module)
	}

	// 列表（按 plan_id 过滤）。
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/plan-modules?plan_id="+p.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("模块列表 status=%d", st)
	}
	var ml struct {
		Items []planbiz.Module `json:"items"`
	}
	_ = json.Unmarshal(raw, &ml)
	if len(ml.Items) != 1 {
		t.Fatalf("模块列表=%d, want 1", len(ml.Items))
	}

	// 删模块。
	st, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/plan-modules/"+mod.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("删模块 status=%d", st)
	}

	// ---- 配额（源无 Get，只有 List/Create/Update/Delete）----
	st, raw = callRaw(t, base, admin, http.MethodPost, "/v1/plan-quotas",
		map[string]any{"plan_id": p.ID, "quota_type": "max_users", "quota_value": 500})
	if st != http.StatusCreated {
		t.Fatalf("加配额 status=%d body=%s", st, raw)
	}
	var q planbiz.Quota
	_ = json.Unmarshal(raw, &q)
	if q.QuotaValue != 500 {
		t.Fatalf("配额值=%d, want 500", q.QuotaValue)
	}

	// 更新配额值。
	st, _ = callRaw(t, base, admin, http.MethodPut, "/v1/plan-quotas/"+q.ID,
		map[string]any{"quota_value": 2000})
	if st != http.StatusOK {
		t.Fatalf("更新配额 status=%d", st)
	}
	quotas, _ := biz.ListQuotas(ctx, p.ID)
	if len(quotas) != 1 || quotas[0].QuotaValue != 2000 {
		t.Fatalf("配额更新后不符: %+v", quotas)
	}
	t.Logf("配额增改正确: %+v", quotas[0])

	// 删配额。
	st, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/plan-quotas/"+q.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("删配额 status=%d", st)
	}
	quotas, _ = biz.ListQuotas(ctx, p.ID)
	if len(quotas) != 0 {
		t.Fatalf("删配额后仍有 %d 条", len(quotas))
	}
}
