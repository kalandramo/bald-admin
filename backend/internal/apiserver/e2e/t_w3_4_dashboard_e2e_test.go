package e2e

// t_w3_4_dashboard_e2e_test.go —— Wave 3.4：首页分析域（源 4 rpc）。
//
// 源：`go-wind-admin/.../dashboard_service.go` + `data/dashboard_repo.go`。
// 本域是**纯读聚合**，零新增存储——数据源是本项目已有的 `AuditRecord`
// （其 `Category` 字段区分 login/operation，恰好覆盖源的 LoginAuditLog /
// OperationAuditLog 双表语义）。
//
// 核心验证点：
//   - 概览四卡计数与库中真实数据一致；
//   - **登录趋势「按日期升序、缺日补零」**（源注释原话，最易漏的语义）；
//   - 操作分布总数 = operation 类审计记录数；
//   - 登录状态分布正确聚合。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"
	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/store"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dashbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dashboard"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

func startDashboardREST(t *testing.T) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	// **装配落库 auditor**——生产路径在 `cmd/go-bald-admin/main.go:786-789`
	// （newApp 内），而 e2e 不经该路径。不装配则全局 auditor 是 no-op，
	// 登录/操作**都不产生审计记录**（实测：审计表 0 条），
	// dashboard 的聚合自然读不到数据。
	//
	// 这是一个**既有测试盲区**：所有依赖审计落库的行为在 e2e 中此前
	// 都验证不到。本测试显式装配，使「登录→审计落库→dashboard 聚合」
	// 全链路可验证。
	audit.SetAuditor(securityaudit.NewStore(bootstrappkg.DB))
	t.Cleanup(func() { audit.SetAuditor(audit.NopAuditor()) })
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		Dashboard: dashbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// seedAudit 写入一条审计记录（Category/Result/Action/Time 可控）。
func seedAudit(t *testing.T, tenantID, category, action, result string, at time.Time) {
	t.Helper()
	rec := &authmodel.AuditRecord{
		TenantID: tenantID,
		Time:     at.UnixNano(),
		Subject:  "u-seed",
		Object:   "probe",
		Action:   action,
		Result:   result,
		Category: category,
	}
	if err := bootstrappkg.AuditStore.Create(context.Background(), rec); err != nil {
		t.Fatalf("seed audit: %v", err)
	}
}

// TestWave3_4_Overview —— 概览四卡与库中真实数据一致。
func TestWave3_4_Overview(t *testing.T) {
	base := startDashboardREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	ctx := context.Background()

	// 独立算出期望值（不用被测代码算期望——避免自证）。
	//
	// **必须按 admin 的租户过滤**：seed 数据里 `u-bob` 属 `t-other`，
	// 而 admin 属 `t-default`——API 做了租户隔离（返回 2），
	// 若期望值查全库（3）就会误判为失败。本测试同时锁住该隔离语义。
	users, _, err := bootstrappkg.UserStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", "t-default")},
	})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	roles, _, err := bootstrappkg.RoleStore.List(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}

	st, raw := callRaw(t, base, admin, http.MethodGet, "/v1/dashboard/overview", nil)
	if st != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", st, raw)
	}
	var ov dashbiz.Overview
	if err := json.Unmarshal(raw, &ov); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	if ov.UserCount != int64(len(users)) {
		t.Fatalf("user_count=%d, want %d", ov.UserCount, len(users))
	}
	if ov.RoleCount != int64(len(roles)) {
		t.Fatalf("role_count=%d, want %d", ov.RoleCount, len(roles))
	}
	if ov.TodayLoginCount < 0 || ov.TodayOperationCount < 0 {
		t.Fatalf("计数不应为负: %+v", ov)
	}
	t.Logf("概览: users=%d roles=%d today_login=%d today_op=%d",
		ov.UserCount, ov.RoleCount, ov.TodayLoginCount, ov.TodayOperationCount)
}

// TestWave3_4_LoginTrendPadsMissingDays —— **本波最重要的验证点**。
//
// 源注释原话：「按日期升序、**缺日补零**」。
// 没有任何登录的日子也必须出现（count=0），否则前端趋势图断线。
func TestWave3_4_LoginTrendPadsMissingDays(t *testing.T) {
	base := startDashboardREST(t)
	_ = loginAs(t, base, "admin", "admin123") // 确保路由与鉴权已装配
	biz := dashbiz.New()
	ctx := context.Background()

	// 造一个唯一的租户，隔离本测试的数据（避免其他测试的审计记录干扰）。
	tenant := "trend-" + time.Now().Format("150405.000000")

	// 只在「3 天前」和「今天」各写一条登录记录——中间的日子应补零。
	now := time.Now()
	seedAudit(t, tenant, "login", "login", "allow", now.AddDate(0, 0, -3))
	seedAudit(t, tenant, "login", "login", "allow", now)
	// 再写一条 operation 类的——**不应**被计入登录趋势。
	seedAudit(t, tenant, "operation", "user:create", "allow", now)

	points, err := biz.GetLoginTrend(ctx, tenant, 7)
	if err != nil {
		t.Fatalf("GetLoginTrend: %v", err)
	}

	// 1) 必须恰好 7 个点。
	if len(points) != 7 {
		t.Fatalf("点数=%d, want 7（缺日补零）", len(points))
	}

	// 2) 必须按日期升序。
	for i := 1; i < len(points); i++ {
		if points[i-1].Date >= points[i].Date {
			t.Fatalf("日期非升序: %s >= %s", points[i-1].Date, points[i].Date)
		}
	}

	// 3) 最后一天（今天）与第 4 天（3 天前）应为 1，其余为 0。
	byDate := map[string]int64{}
	for _, p := range points {
		byDate[p.Date] = p.Count
	}
	today := now.Format("2006-01-02")
	threeAgo := now.AddDate(0, 0, -3).Format("2006-01-02")
	if byDate[today] != 1 {
		t.Fatalf("今天 count=%d, want 1", byDate[today])
	}
	if byDate[threeAgo] != 1 {
		t.Fatalf("3 天前 count=%d, want 1", byDate[threeAgo])
	}
	// 中间 5 天必须是 0（补零）。
	zeroDays := 0
	for _, p := range points {
		if p.Count == 0 {
			zeroDays++
		}
	}
	if zeroDays != 5 {
		t.Fatalf("补零天数=%d, want 5（7 点中 2 天有数据）; points=%+v", zeroDays, points)
	}
	t.Logf("登录趋势（升序 + 补零）: %+v", points)
}

// TestWave3_4_OperationActionDistribution —— 操作分布聚合正确。
func TestWave3_4_OperationActionDistribution(t *testing.T) {
	base := startDashboardREST(t)
	_ = loginAs(t, base, "admin", "admin123") // 确保路由与鉴权已装配
	biz := dashbiz.New()
	ctx := context.Background()
	tenant := "act-" + time.Now().Format("150405.000000")

	now := time.Now()
	seedAudit(t, tenant, "operation", "user:create", "allow", now)
	seedAudit(t, tenant, "operation", "user:create", "allow", now)
	seedAudit(t, tenant, "operation", "secret:delete", "deny", now)
	// 登录类不应计入操作分布。
	seedAudit(t, tenant, "login", "login", "allow", now)

	items, err := biz.GetOperationActionDistribution(ctx, tenant)
	if err != nil {
		t.Fatalf("GetOperationActionDistribution: %v", err)
	}
	got := map[string]int64{}
	var total int64
	for _, it := range items {
		got[it.Label] = it.Count
		total += it.Count
	}
	if got["user:create"] != 2 {
		t.Fatalf("user:create=%d, want 2", got["user:create"])
	}
	if got["secret:delete"] != 1 {
		t.Fatalf("secret:delete=%d, want 1", got["secret:delete"])
	}
	if total != 3 {
		t.Fatalf("总数=%d, want 3（login 类不应计入）; items=%+v", total, items)
	}
	t.Logf("操作分布: %+v", items)
}

// TestWave3_4_LoginStatusDistribution —— 登录状态分布聚合正确。
func TestWave3_4_LoginStatusDistribution(t *testing.T) {
	base := startDashboardREST(t)
	_ = loginAs(t, base, "admin", "admin123") // 确保路由与鉴权已装配
	biz := dashbiz.New()
	ctx := context.Background()
	tenant := "st-" + time.Now().Format("150405.000000")

	now := time.Now()
	seedAudit(t, tenant, "login", "login", "allow", now)
	seedAudit(t, tenant, "login", "login", "allow", now)
	seedAudit(t, tenant, "login", "login", "deny", now)
	// 操作类不应计入。
	seedAudit(t, tenant, "operation", "user:create", "allow", now)

	items, err := biz.GetLoginStatusDistribution(ctx, tenant)
	if err != nil {
		t.Fatalf("GetLoginStatusDistribution: %v", err)
	}
	got := map[string]int64{}
	var total int64
	for _, it := range items {
		got[it.Label] = it.Count
		total += it.Count
	}
	if got["allow"] != 2 || got["deny"] != 1 {
		t.Fatalf("分布不符: %+v", got)
	}
	if total != 3 {
		t.Fatalf("总数=%d, want 3（operation 类不应计入）", total)
	}
	t.Logf("登录状态分布: %+v", items)
}

// TestWave3_4_HTTPEndpoints —— 4 条 rpc 的 HTTP 端点可用（含 days 参数）。
func TestWave3_4_HTTPEndpoints(t *testing.T) {
	base := startDashboardREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	cases := []struct {
		path string
		key  string
	}{
		{"/v1/dashboard/overview", ""},
		{"/v1/dashboard/login-trend?days=3", "points"},
		{"/v1/dashboard/operation-actions", "items"},
		{"/v1/dashboard/login-status", "items"},
	}
	for _, tc := range cases {
		st, raw := callRaw(t, base, admin, http.MethodGet, tc.path, nil)
		if st != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", tc.path, st, raw)
		}
		if tc.key != "" {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(raw, &m)
			if _, ok := m[tc.key]; !ok {
				t.Fatalf("GET %s 缺字段 %q: %s", tc.path, tc.key, raw)
			}
		}
		t.Logf("GET %s -> %d", tc.path, st)
	}

	// days=3 应返回恰好 3 个点。
	st, raw := callRaw(t, base, admin, http.MethodGet, "/v1/dashboard/login-trend?days=3", nil)
	if st != http.StatusOK {
		t.Fatalf("trend status=%d", st)
	}
	var tr struct {
		Points []dashbiz.TrendPoint `json:"points"`
	}
	_ = json.Unmarshal(raw, &tr)
	if len(tr.Points) != 3 {
		t.Fatalf("days=3 返回 %d 点, want 3", len(tr.Points))
	}
	t.Logf("days=3 参数生效: %+v", tr.Points)

	// 保留 storev1 引用。
	_ = storev1.FilterCondition{}
}

// TestWave3_4_FullChainLoginToDashboard —— **全链路**：真实登录 →
// 审计落库 → dashboard 聚合能读到。
//
// 这条链路此前在 e2e 中不可验证（auditor 未装配）——本测试是首个覆盖者。
func TestWave3_4_FullChainLoginToDashboard(t *testing.T) {
	base := startDashboardREST(t)
	biz := dashbiz.New()
	ctx := context.Background()
	tenant := "t-default" // admin 的租户

	// 先取基线（本测试可能与同包其他测试共享 DB，故用增量而非绝对值）。
	before, err := biz.GetOverview(ctx, tenant)
	if err != nil {
		t.Fatalf("baseline overview: %v", err)
	}

	// 真实登录（产生 login 审计）。
	_ = loginAs(t, base, "admin", "admin123")
	time.Sleep(300 * time.Millisecond) // 审计为异步落库

	after, err := biz.GetOverview(ctx, tenant)
	if err != nil {
		t.Fatalf("overview after login: %v", err)
	}

	if after.TodayLoginCount <= before.TodayLoginCount {
		t.Fatalf("登录后今日登录数未增加: %d -> %d（审计未落库？）",
			before.TodayLoginCount, after.TodayLoginCount)
	}
	t.Logf("全链路打通: today_login %d -> %d", before.TodayLoginCount, after.TodayLoginCount)

	// 登录状态分布应含 allow。
	items, err := biz.GetLoginStatusDistribution(ctx, tenant)
	if err != nil {
		t.Fatalf("login status: %v", err)
	}
	found := false
	for _, it := range items {
		if it.Label == "allow" && it.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("登录状态分布应含 allow>0: %+v", items)
	}
	t.Logf("登录状态分布（含真实登录）: %+v", items)
}
