package e2e

// t_w4_1_permgroup_e2e_test.go —— Wave 4.1：权限组 + 策略评估日志（源 7 rpc）。
//
// 源：`i_permission_group.proto`（5 rpc）+ `i_policy_evaluation_log.proto`（2 rpc）。
//
// ## 核心语义（本波重点）
//
// **权限组的物化路径格式**——源 description 原文
// （`permission_group.proto:53-56`）：「树形路径，格式：/1/10/101/
// （**包含自身且首尾带/**）」。
//
// 本测试专门锁住该格式（含自身、首尾带 `/`、父段续接），
// 因为这是最容易「看起来对但格式不符」的地方——e2e 逐字节断言。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

func startPermGroupREST(t *testing.T) string {
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
		PermGroup: pgbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func pgSuffix() string { return "pg" + time.Now().Format("150405.000000") }

// TestWave4_1_GroupTreePathFormat —— **本波最重要的验证点**。
//
// 锁住源语义：path 格式 `/父段/自身段/`——**含自身、首尾带 `/`**。
func TestWave4_1_GroupTreePathFormat(t *testing.T) {
	base := startPermGroupREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	sfx := pgSuffix()

	// 1) 创建根节点。
	rootCode := "root-" + sfx
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": rootCode, "name": "根分组", "module": "opm", "status": "ON"})
	if st != http.StatusCreated {
		t.Fatalf("create root status=%d body=%s", st, raw)
	}
	var root pgbiz.Group
	_ = json.Unmarshal(raw, &root)

	// 根节点 path 必须是 "/<code>/"（首尾带 /、含自身）。
	wantRoot := "/" + rootCode + "/"
	if root.Path != wantRoot {
		t.Fatalf("根 path=%q, want %q（首尾带 /、含自身）", root.Path, wantRoot)
	}
	t.Logf("根节点 path 格式正确: %s", root.Path)

	// 2) 创建子节点（挂在根下）。
	childCode := "child-" + sfx
	st, raw = callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": childCode, "name": "子分组", "parent_id": root.ID})
	if st != http.StatusCreated {
		t.Fatalf("create child status=%d body=%s", st, raw)
	}
	var child pgbiz.Group
	_ = json.Unmarshal(raw, &child)

	// 子 path 必须是 "/<rootCode>/<childCode>/"（父段续接 + 自身 + 尾 /）。
	wantChild := "/" + rootCode + "/" + childCode + "/"
	if child.Path != wantChild {
		t.Fatalf("子 path=%q, want %q", child.Path, wantChild)
	}
	if !strings.HasSuffix(child.Path, "/") {
		t.Fatalf("子 path 未以 / 结尾: %q（源格式要求首尾带 /）", child.Path)
	}
	t.Logf("子节点 path 格式正确: %s", child.Path)

	// 3) 树形列表：子应挂在根的 children 下。
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/permission-groups", nil)
	if st != http.StatusOK {
		t.Fatalf("list status=%d", st)
	}
	var tree struct {
		Items []*pgbiz.Group `json:"items"`
	}
	_ = json.Unmarshal(raw, &tree)

	var foundRoot *pgbiz.Group
	for _, g := range tree.Items {
		if g.ID == root.ID {
			foundRoot = g
		}
	}
	if foundRoot == nil {
		t.Fatal("树形列表未找到根节点")
	}
	if len(foundRoot.Children) != 1 || foundRoot.Children[0].ID != child.ID {
		t.Fatalf("子节点未挂在根下: children=%+v", foundRoot.Children)
	}
	t.Logf("树形结构正确：根 %s 有 %d 个子节点", root.Name, len(foundRoot.Children))

	// 4) 清理（先子后父）。
	if st, _ := callRaw(t, base, admin, http.MethodDelete, "/v1/permission-groups/"+child.ID, nil); st != http.StatusOK {
		t.Fatalf("delete child status=%d", st)
	}
	if st, _ := callRaw(t, base, admin, http.MethodDelete, "/v1/permission-groups/"+root.ID, nil); st != http.StatusOK {
		t.Fatalf("delete root status=%d", st)
	}
}

// TestWave4_1_DeleteWithChildrenRejected —— 有子节点时拒绝删除（防悬挂）。
func TestWave4_1_DeleteWithChildrenRejected(t *testing.T) {
	base := startPermGroupREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	biz := pgbiz.New()
	ctx := context.Background()
	sfx := pgSuffix()

	root, err := biz.CreateGroup(ctx, "t-default", "dr-"+sfx, pgbiz.Group{Name: "父"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	child, err := biz.CreateGroup(ctx, "t-default", "dc-"+sfx, pgbiz.Group{Name: "子", ParentID: root.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// 删父 → 400（有子节点）。
	st, raw := callRaw(t, base, admin, http.MethodDelete, "/v1/permission-groups/"+root.ID, nil)
	if st != http.StatusBadRequest {
		t.Fatalf("删除有子节点的父应 400，实际 %d body=%s", st, raw)
	}
	t.Logf("确认：有子节点时拒绝删除（%d）", st)

	// 清理。
	_ = biz.DeleteGroup(ctx, child.ID)
	_ = biz.DeleteGroup(ctx, root.ID)
}

// TestWave4_1_CreateValidation —— 入参校验。
func TestWave4_1_CreateValidation(t *testing.T) {
	base := startPermGroupREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	sfx := pgSuffix()

	// 缺 name → 400。
	st, _ := callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": "x-" + sfx})
	if st != http.StatusBadRequest {
		t.Fatalf("缺 name status=%d, want 400", st)
	}

	// 缺 code → 400。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"name": "只有名字"})
	if st != http.StatusBadRequest {
		t.Fatalf("缺 code status=%d, want 400", st)
	}

	// 父不存在 → 400（避免悬挂引用）。
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": "orphan-" + sfx, "name": "孤儿", "parent_id": "no-such-parent"})
	if st != http.StatusBadRequest {
		t.Fatalf("父不存在应 400，实际 %d body=%s", st, raw)
	}
	t.Log("确认：父不存在时拒绝创建（避免悬挂引用）")

	// 重复 code → 409。
	code := "dup-" + sfx
	if st, _ := callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": code, "name": "第一次"}); st != http.StatusCreated {
		t.Fatalf("首次创建 status=%d", st)
	}
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/permission-groups",
		map[string]any{"code": code, "name": "重复"})
	if st != http.StatusConflict {
		t.Fatalf("重复 code status=%d, want 409", st)
	}
	t.Log("确认：重复 code 返回 409")
}

// TestWave4_1_EvalLogRead —— 策略评估日志的读写（List/Get）。
func TestWave4_1_EvalLogRead(t *testing.T) {
	base := startPermGroupREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	biz := pgbiz.New()
	ctx := context.Background()

	// 先取基线（同包测试共享 DB，用增量断言）。
	before, err := biz.ListEvalLogs(ctx, "t-default")
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}

	// 写两条：一条允许、一条拒绝。
	if err := biz.RecordEvaluation(ctx, "t-default", "u-admin", "secret:get",
		"/v1/secret/1", "GET", true, ""); err != nil {
		t.Fatalf("record allow: %v", err)
	}
	if err := biz.RecordEvaluation(ctx, "t-default", "u-alice", "secret:delete",
		"/v1/secret/1", "DELETE", false, "policy denied"); err != nil {
		t.Fatalf("record deny: %v", err)
	}

	after, err := biz.ListEvalLogs(ctx, "t-default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) < len(before)+2 {
		t.Fatalf("评估日志未落库: %d -> %d", len(before), len(after))
	}

	// 校验内容（不只看数量）。
	var allow, deny *pgbiz.EvalLog
	for i := range after {
		e := &after[i]
		if e.PermissionID == "secret:get" && e.UserID == "u-admin" {
			allow = e
		}
		if e.PermissionID == "secret:delete" && e.UserID == "u-alice" {
			deny = e
		}
	}
	if allow == nil || !allow.Result {
		t.Fatalf("未找到 allow 记录: %+v", allow)
	}
	if deny == nil || deny.Result {
		t.Fatalf("未找到 deny 记录: %+v", deny)
	}
	if deny.EffectDetails != "policy denied" {
		t.Fatalf("拒绝原因不符: %q", deny.EffectDetails)
	}
	t.Logf("评估日志正确：allow(%s) / deny(%s, %s)",
		allow.RequestPath, deny.RequestPath, deny.EffectDetails)

	// HTTP 端点 + Get 单条。
	st, raw := callRaw(t, base, admin, http.MethodGet, "/v1/policy-evaluation-logs", nil)
	if st != http.StatusOK {
		t.Fatalf("HTTP list status=%d body=%s", st, raw)
	}
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/policy-evaluation-logs/"+allow.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("HTTP get status=%d body=%s", st, raw)
	}
	var got pgbiz.EvalLog
	_ = json.Unmarshal(raw, &got)
	if got.ID != allow.ID || !got.Result {
		t.Fatalf("get 返回不符: %+v", got)
	}
	t.Log("HTTP 端点（List/Get）验证通过")

	// 不存在的 id → 404。
	st, _ = callRaw(t, base, admin, http.MethodGet, "/v1/policy-evaluation-logs/no-such-id", nil)
	if st != http.StatusNotFound {
		t.Fatalf("不存在的 id status=%d, want 404", st)
	}
}

// TestWave4_1_Update —— 更新字段。
func TestWave4_1_Update(t *testing.T) {
	base := startPermGroupREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	biz := pgbiz.New()
	ctx := context.Background()
	sfx := pgSuffix()

	g, err := biz.CreateGroup(ctx, "t-default", "up-"+sfx, pgbiz.Group{Name: "原名", Module: "opm"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	st, _ := callRaw(t, base, admin, http.MethodPut, "/v1/permission-groups/"+g.ID,
		map[string]any{"name": "改名后", "module": "order", "status": "OFF", "sort_order": 5})
	if st != http.StatusOK {
		t.Fatalf("update status=%d", st)
	}

	got, err := biz.GetGroup(ctx, g.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "改名后" || got.Module != "order" || got.Status != "OFF" || got.SortOrder != 5 {
		t.Fatalf("更新后不符: %+v", got)
	}
	// **path 不应被更新影响**（它是结构属性，非可改字段）。
	if got.Path != g.Path {
		t.Fatalf("path 被意外改写: %q -> %q", g.Path, got.Path)
	}
	t.Logf("更新正确，path 保持: %s", got.Path)

	_ = biz.DeleteGroup(ctx, g.ID)
}
