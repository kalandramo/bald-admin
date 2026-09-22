package e2e

// t6_audit_tenant_isolation_e2e_test.go —— 审计查询的租户隔离（2026-09-22
// 统一分页风格引入的行为变更）。

import (
	"net/http"
	"testing"
	"time"
)

// TestAuditREST_TenantIsolation 审计查询的租户隔离（2026-09-22 统一分页风格
// 引入的行为变更）。
//
// 背景：本域原为「跨租户全量查询」（Where 不注入租户条件）。统一分页后走
// Store.ListWithPaging → translate → mergeTenant 强制注入 tenant_id 条件，
// 框架无跳过机制。本测试锁定新语义：**只能看到本租户的审计记录**。
//
// 同时验证空租户兜底：登录失败（用户不存在）事件天然无租户，落库时经
// RecordMapper.DefaultTenantID 兜底为 t-default——故 t-default 租户能看到
// 这些「谁在试密码」的线索（t6 的 LoginAuditFields 已覆盖），而其他租户看不到。
func TestAuditREST_TenantIsolation(t *testing.T) {
	base := startAuditREST(t)
	// t-default 租户的管理员。
	tokDefault := tenantToken(t, "admin", "u-admin", "admin", "t-default")
	// 另一个租户（种子数据里的 t-other）。
	tokOther := tenantToken(t, "admin", "u-admin", "admin", "t-other")

	// t-default 租户下造一条可识别的审计（唯一 UA 便于精确定位）。
	ua := "t6-iso-agent-" + time.Now().Format("150405.000000")
	if code, _ := postJSON(t, base, http.MethodPost, "/v1/login", ua, "",
		`{"username":"t6-iso-user","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("bad login status=%d", code)
	}

	// 同租户（t-default）可见。
	seen := listAudit(t, base, tokDefault, "?category=login&subject=t6-iso-user")
	if seen.GetMeta().GetTotal().GetValue() == 0 {
		t.Fatalf("t-default 应能看到本租户的登录失败审计（subject=t6-iso-user）")
	}

	// 跨租户（t-other）不可见——隔离生效的核心断言。
	cross := listAudit(t, base, tokOther, "?category=login&subject=t6-iso-user")
	if cross.GetMeta().GetTotal().GetValue() != 0 {
		t.Fatalf("t-other 不应看到 t-default 的审计，实得 total=%d",
			cross.GetMeta().GetTotal().GetValue())
	}
	t.Logf("租户隔离生效 ✅（同租户可见 / 跨租户不可见）")
}
