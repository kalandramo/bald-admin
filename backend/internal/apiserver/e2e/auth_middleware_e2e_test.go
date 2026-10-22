package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	appauthz "github.com/kalandramo/bald-admin/internal/security/authz"
)

// setup 初始化桥接并构造带完整路由的 gin engine。
func setup(t *testing.T) *gingonic.Engine {
	t.Helper()
	gingonic.SetMode(gingonic.TestMode)
	if _, err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	repos = bootstrappkg.LastRepositories()
	e := gingonic.New()
	// 域路径形态——共面挂载 + 逐域 Register*，
	// biz 仍由本测试当场直构（隔离价值不变）。
	registerCoreFaces(t, e, nil, nil, nil, nil)
	return e
}

func login(t *testing.T, e *gingonic.Engine, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(authbiz.Credential{Username: username, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s: got %d, body=%s", username, w.Code, w.Body.String())
	}
	var pair authbiz.TokenPair
	if err := json.Unmarshal(w.Body.Bytes(), &pair); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return pair.AccessToken
}

func do(e *gingonic.Engine, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

// TestAuthnRequired_NoToken 未携带 token 访问受保护资源应 401。
func TestAuthnRequired_NoToken(t *testing.T) {
	e := setup(t)
	w := do(e, http.MethodGet, "/v1/secret/s-db-pwd", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestAuthz_Admin_FullAccess admin 可读可删。
func TestAuthz_Admin_FullAccess(t *testing.T) {
	e := setup(t)
	tok := login(t, e, "admin", "admin123")

	if w := do(e, http.MethodGet, "/v1/secret/s-db-pwd", tok); w.Code != http.StatusOK {
		t.Fatalf("admin GET secret: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	if w := do(e, http.MethodDelete, "/v1/secret/s-db-pwd", tok); w.Code != http.StatusOK {
		t.Fatalf("admin DELETE secret: want 200 got %d (%s)", w.Code, w.Body.String())
	}
}

// TestAuthz_Viewer_ForbiddenDelete viewer 可读但删除应 403。
// 注意：admin 用例会删除 s-db-pwd，为避免用例间 seed 数据污染，viewer 只读未被删除的 s-api-key。
func TestAuthz_Viewer_ForbiddenDelete(t *testing.T) {
	e := setup(t)
	tok := login(t, e, "alice", "alice123")

	if w := do(e, http.MethodGet, "/v1/secret/s-api-key", tok); w.Code != http.StatusOK {
		t.Fatalf("viewer GET secret: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	if w := do(e, http.MethodDelete, "/v1/secret/s-api-key", tok); w.Code != http.StatusForbidden {
		t.Fatalf("viewer DELETE secret: want 403 got %d (%s)", w.Code, w.Body.String())
	}
}

// TestDeleteSecret_RealDelete admin 删除后该 secret 真正从 store 消失（CR 修复：
// DELETE 不再返回占位 {"deleted":id}，而是经 biz 落 store，并清空缓存条目）。
// 为避免与共享 seed 用例相互污染（顺序无关），本用例自建一条专属 secret（t-default 租户）后删除。
func TestDeleteSecret_RealDelete(t *testing.T) {
	e := setup(t)
	tok := login(t, e, "admin", "admin123")

	// 自建专属资源（admin 属 t-default，落同租户，受隔离约束可命中）。
	own := &authmodel.Secret{ID: "s-del-temp", Name: "待删资源", Content: "x", TenantID: "t-default"}
	if err := repos.Secret.Create(context.Background(), own); err != nil {
		t.Fatalf("seed temp secret: %v", err)
	}

	if w := do(e, http.MethodDelete, "/v1/secret/s-del-temp", tok); w.Code != http.StatusOK {
		t.Fatalf("admin DELETE secret: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	// 删除后再次 GET 应 404（资源已真实消失，而非占位响应）。
	if w := do(e, http.MethodGet, "/v1/secret/s-del-temp", tok); w.Code != http.StatusNotFound {
		t.Fatalf("admin GET after delete: want 404 got %d (%s)", w.Code, w.Body.String())
	}
	// 删除后再次 DELETE 同 ID 应 404（幂等 NotFound，不报错）。
	if w := do(e, http.MethodDelete, "/v1/secret/s-del-temp", tok); w.Code != http.StatusNotFound {
		t.Fatalf("admin DELETE again: want 404 got %d (%s)", w.Code, w.Body.String())
	}
}

// TestBadCredential 错误密码应 401。
func TestBadCredential(t *testing.T) {
	e := setup(t)
	body, _ := json.Marshal(authbiz.Credential{Username: "admin", Password: "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

// TestWhoAmI 登录后可解析当前用户。
func TestWhoAmI(t *testing.T) {
	e := setup(t)
	tok := login(t, e, "admin", "admin123")
	w := do(e, http.MethodGet, "/v1/auth/whoami", tok)
	if w.Code != http.StatusOK {
		t.Fatalf("whoami: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	var info authbiz.UserInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	if info.UserID != "u-admin" {
		t.Fatalf("want u-admin, got %s", info.UserID)
	}
}

// TestAuthzChain_EvalLogsRecorded 判定链路的策略评估日志真实落库（接线验收）。
//
// 接线前的缺陷：读面（ListEvalLogs/GetEvalLog + GET /v1/policy-evaluation-logs）、
// 表与仓储全量就位，写入方 Biz.RecordEvaluation 却从未接进判定链路 → 端点恒返回
// {"items":[]}。本用例沿既有 401→403→200 闭环走**真实 HTTP 请求**，断言：
//   - 401（认证层即拦，判定根本没跑）不产生日志；
//   - viewer 读 200 → 一条 allow，viewer 删 403 → 一条 deny（Result bool 两态都用）；
//   - 字段取自判定三元组（tenant/user/obj/method/result/effect_details）；
//   - GET /v1/policy-evaluation-logs 从恒空变为真实数据。
//
// 写入面注入与生产同源同形：生产在 cmd/bald-admin/app/assembly.go 的 BeforeStart 内
// 调 SetEvalRecorder 迟到取用 permgroupd.Biz()；本包按既有纪律当场直构 biz
// （域 Wire 不在 e2e 路径上，见 corefaces_test.go 头注），故注入当场构造的实例。
func TestAuthzChain_EvalLogsRecorded(t *testing.T) {
	e := setup(t)
	pg := pgbiz.New(repos.PermGroup, repos.PolicyEvalLog)
	bootstrappkg.SetEvalRecorder(func() appauthz.EvaluationRecorder { return pg })
	t.Cleanup(func() { bootstrappkg.SetEvalRecorder(nil) })

	ctx := context.Background()
	base, err := pg.ListEvalLogs(ctx, "t-default")
	if err != nil {
		t.Fatalf("baseline ListEvalLogs: %v", err)
	}
	seen := make(map[string]bool, len(base))
	for i := range base {
		seen[base[i].ID] = true
	}

	// 1) 无 token → 401：认证层拦下，不进判定。
	if w := do(e, http.MethodGet, "/v1/secret/s-api-key", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401 got %d (%s)", w.Code, w.Body.String())
	}
	if cur, err := pg.ListEvalLogs(ctx, "t-default"); err != nil {
		t.Fatalf("list after 401: %v", err)
	} else if len(cur) != len(base) {
		t.Fatalf("401 不应产生评估日志: %d -> %d", len(base), len(cur))
	}

	// 2) viewer：读放行 / 删拒绝。
	viewer := login(t, e, "alice", "alice123")
	if w := do(e, http.MethodGet, "/v1/secret/s-api-key", viewer); w.Code != http.StatusOK {
		t.Fatalf("viewer GET: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	if w := do(e, http.MethodDelete, "/v1/secret/s-api-key", viewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer DELETE: want 403 got %d (%s)", w.Code, w.Body.String())
	}
	// 3) admin：读放行。
	admin := login(t, e, "admin", "admin123")
	if w := do(e, http.MethodGet, "/v1/secret/s-api-key", admin); w.Code != http.StatusOK {
		t.Fatalf("admin GET: want 200 got %d (%s)", w.Code, w.Body.String())
	}

	// —— 三次判定 = 三条新日志，allow 与 deny 各至少一条 ——
	after, err := pg.ListEvalLogs(ctx, "t-default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var fresh []pgbiz.EvalLog
	for i := range after {
		if !seen[after[i].ID] {
			fresh = append(fresh, after[i])
		}
	}
	if len(fresh) != 3 {
		t.Fatalf("新增评估日志 %d 条, want 3: %+v", len(fresh), fresh)
	}
	var allow, deny *pgbiz.EvalLog
	for i := range fresh {
		l := &fresh[i]
		switch {
		case l.UserID == "u-alice" && l.Result && l.RequestMethod == "get":
			allow = l
		case l.UserID == "u-alice" && !l.Result && l.RequestMethod == "delete":
			deny = l
		}
	}
	if allow == nil || deny == nil {
		t.Fatalf("未同时找到 viewer 的 allow/deny 日志: %+v", fresh)
	}
	// 字段取法：obj 即 DefaultHTTPObject 解析出的资源标识（ctx 不携带原始 path，
	// 见 internal/security/authz/recording.go 头注「字段取法」）。
	for _, l := range []*pgbiz.EvalLog{allow, deny} {
		if l.PermissionID != "secret" || l.RequestPath != "secret" {
			t.Fatalf("obj 字段不符: %+v", l)
		}
		if l.EffectDetails != "roles=viewer" {
			t.Fatalf("effect_details 应记主体角色快照, got %q", l.EffectDetails)
		}
	}

	// tenant 不在对外 DTO 里（模型列），经仓储直查——顺带锁 ID 前缀格式。
	for _, l := range []*pgbiz.EvalLog{allow, deny} {
		row, err := repos.PolicyEvalLog.Get(ctx, mustWhere("id", l.ID))
		if err != nil {
			t.Fatalf("get row %s: %v", l.ID, err)
		}
		if row.TenantID != "t-default" {
			t.Fatalf("tenant_id=%q, want t-default", row.TenantID)
		}
		if !strings.HasPrefix(row.ID, "t-default:pel:") {
			t.Fatalf("ID 前缀格式不符: %q", row.ID)
		}
	}
	t.Logf("真实评估日志样例: id=%s tenant=t-default user=%s obj=%s method=%s result=%v details=%q",
		deny.ID, deny.UserID, deny.PermissionID, deny.RequestMethod, deny.Result, deny.EffectDetails)

	// 4) 读面端点从恒空变为真实数据（permgroup 路由在本用例内挂载）。
	hgin.RegisterPermGroup(e, bootstrappkg.Authenticator, bootstrappkg.Authorizer, pg)
	w := do(e, http.MethodGet, "/v1/policy-evaluation-logs", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/policy-evaluation-logs: want 200 got %d (%s)", w.Code, w.Body.String())
	}
	var list struct {
		Items []pgbiz.EvalLog `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatal("端点仍恒空——写入方未接进判定链路")
	}
	var foundDeny bool
	for _, it := range list.Items {
		if it.ID == deny.ID {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Fatalf("端点未返回刚才那条 deny 日志（共 %d 条）", len(list.Items))
	}
	t.Logf("GET /v1/policy-evaluation-logs 返回 %d 条真实日志", len(list.Items))
}
