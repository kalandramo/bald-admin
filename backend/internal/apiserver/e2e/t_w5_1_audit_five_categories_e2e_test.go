package e2e

// t_w5_1_audit_five_categories_e2e_test.go —— 审计五类落库与查询。
//
// ## 背景
//
// 审计分**五类**（operation / login / api / data_access / permission），
// 本项目用**单表 + category** 建模：五类共用一张表，以 category 区分。
//
// 决策：**保持单表 + category**，把五类差异字段扩为 nullable 列。理由：
// 查询面已统一；拆五表要新增多套 model + store + handler，代价大
// 而收益仅是表结构对齐。能力验证的重点是「五类事件能落库 + 能查询」。
//
// ## 本测试锁定
//
//  1. **api 类自动采集**：任一 HTTP 请求经中间件链后，落一条 category=api
//     审计，含 http_method / path / status_code（框架 gin 审计中间件只填
//     status，缺的 method/path 由本项目补齐）。
//  2. **五类可查**：category 过滤能分别取到 api / data_access / permission
//     三类。
//  3. **permission 类**：权限变更落 category=permission 审计，
//     含 target_type / old_value / new_value 语义字段。

import (
	"net/http"
	"testing"
	"time"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
)

// TestWave5_1_APICategoryAutoCollected —— api 类审计由中间件自动采集。
//
// 修复前：category=api 查询返回 0 条（无任何路径产生 api 类审计）。
func TestWave5_1_APICategoryAutoCollected(t *testing.T) {
	base := startAuditREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	// 发一个业务请求（唯一 UA 隔离数据），触发审计中间件。
	ua := "w5-api-agent-" + time.Now().Format("150405.000000")
	if code, raw := postJSON(t, base, http.MethodGet, "/v1/tenant", ua, tok, ""); code != http.StatusOK {
		t.Fatalf("GET /v1/tenant status=%d body=%s", code, raw)
	}

	// api 类审计应被采集（修复前 total=0 → RED）。
	list := listAudit(t, base, tok, "?category=api")
	var hit *auditv1.AuditRecord
	for _, it := range list.GetItems() {
		if it.GetUserAgent() == ua {
			hit = it
			break
		}
	}
	if hit == nil {
		t.Fatalf("category=api 未采集到本次请求的审计（UA=%s），total=%d", ua, list.GetMeta().GetTotal().GetValue())
	}
	// api 类专属字段：method/path/status_code。
	if hit.GetHttpMethod() != "GET" {
		t.Fatalf("api audit http_method=%q, want GET", hit.GetHttpMethod())
	}
	if hit.GetPath() != "/v1/tenant" {
		t.Fatalf("api audit path=%q, want /v1/tenant", hit.GetPath())
	}
	if hit.GetStatusCode() != http.StatusOK {
		t.Fatalf("api audit status_code=%d, want 200", hit.GetStatusCode())
	}
	t.Logf("api 类审计采集正确: method=%s path=%s status=%d",
		hit.GetHttpMethod(), hit.GetPath(), hit.GetStatusCode())
}

// TestWave5_1_DataAccessCategory —— data_access 类审计（SQL 执行被采集）。
//
// 依赖 gorm callback 的 SQL 采集机制。本测试只断言「有 data_access
// 类记录且含 table_name」。
func TestWave5_1_DataAccessCategory(t *testing.T) {
	base := startAuditREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	// 触发一次真实 DB 写（创建租户 → INSERT）。
	body := `{"id":"t-w5-da","name":"w5-da","remark":"w5"}`
	if code, raw := postJSON(t, base, http.MethodPost, "/v1/tenant", "w5-da-agent", tok, body); code != http.StatusCreated {
		t.Fatalf("create tenant status=%d body=%s", code, raw)
	}

	// 等待异步采集（若有）。
	time.Sleep(200 * time.Millisecond)
	list := listAudit(t, base, tok, "?category=data_access")
	if list.GetMeta().GetTotal().GetValue() == 0 {
		t.Fatalf("category=data_access 无记录（SQL 采集未接线，Wave 5.2 目标）")
	}
	rec := list.GetItems()[0]
	if rec.GetTableName() == "" {
		t.Fatalf("data_access 记录缺 table_name: %+v", rec)
	}
	t.Logf("data_access 类审计采集正确: table=%s db_user=%s", rec.GetTableName(), rec.GetDbUser())
}

// TestWave5_1_PermissionCategory —— permission 类审计（权限变更被采集）。
//
// ## 语义（关键，修正了首版测试的错误假设）
//
// permission 类审计的 ActionType 是 **GRANT/REVOKE/ASSIGN/EXPIRE** 等
// **权限变更**语义，**不是**授权拒绝。写入由**权限变更操作**触发。
//
// 故本测试触发**策略行增删**（`POST/DELETE /v1/permission/policy`，
// 即 GRANT/REVOKE 语义），断言落 category=permission 审计。
func TestWave5_1_PermissionCategory(t *testing.T) {
	base := startAuditREST(t)
	admin := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	// GRANT 语义：新增一条策略行（给 viewer 授予 secret:get）。
	body := `{"role":"viewer","object":"w5-target","action":"get"}`
	if code, raw := postJSON(t, base, http.MethodPost, "/v1/permission/policy", "w5-perm-agent", admin, body); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("POST /v1/permission/policy status=%d body=%s", code, raw)
	}

	list := listAudit(t, base, admin, "?category=permission")
	if list.GetMeta().GetTotal().GetValue() == 0 {
		t.Fatalf("category=permission 无记录（权限变更未接审计）")
	}
	rec := list.GetItems()[0]
	if rec.GetTargetType() == "" {
		t.Fatalf("permission 记录缺 target_type: %+v", rec)
	}
	t.Logf("permission 类审计采集正确: target_type=%s target_id=%s result=%s",
		rec.GetTargetType(), rec.GetTargetId(), rec.GetResult())
}
