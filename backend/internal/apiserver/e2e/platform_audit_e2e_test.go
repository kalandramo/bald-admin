package e2e

// platform_audit_e2e_test.go 平台级审计视图 e2e（bald v0.13.0 平台身份能力接入）。
//
// 验收目标：**同一份审计数据，按身份分流**
//   - 平台身份（AuthClaims.Platform=true）→ 跨租户可见（平台级视图）
//   - 租户身份（Platform 缺省 false）    → 仅本租户可见（隔离生效）
//
// 依据：bald v0.13.0 引入身份级平台豁免（store.shouldIsolate 的
// contextx.PlatformFromContext 分支）——与实体级 WithPlatformLevel 正交。
//
// 数据隔离：本用例自造审计记录（直接经 StoreAuditor 落库，绕开 HTTP），
// 用唯一 tenant 值避免与其他用例累计数据串扰。

import (
	"context"
	"testing"
	"time"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/contextx"
)

// platformToken 签发带 Platform 标记的访问令牌（平台级身份）。
//
// 与 tenantToken 的唯一差别是 Platform=true——这是 bald v0.13.0 新增的
// **显式**平台身份声明（绝不基于空租户推断）。
func platformToken(t *testing.T, username, userID, tenantID string) string {
	t.Helper()
	claims := authn.AuthClaims{
		Issuer:   "go-bald-admin",
		Subject:  userID,
		TenantID: tenantID,
		Roles:    []string{"admin"},
		Name:     username,
		Platform: true, // ← 平台级身份
	}
	tok, err := bootstrappkg.Signer.IssueToken(claims, 2*time.Hour)
	if err != nil {
		t.Fatalf("issue platform token: %v", err)
	}
	return tok
}

// seedAuditForTenant 直接落一条审计记录到指定租户（绕开 HTTP，供隔离对比用）。
func seedAuditForTenant(t *testing.T, tenantID, subject string) {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	audit.SetAuditor(securityaudit.NewStore(bootstrappkg.DB))
	t.Cleanup(func() { audit.SetAuditor(audit.NopAuditor()) })

	audit.GetAuditor().Record(context.Background(), audit.AuditEvent{
		Subject:  subject,
		TenantID: tenantID,
		Object:   "platform-view-probe",
		Action:   "list",
		Result:   audit.ResultAllow,
	})
}

// TestAudit_TenantIsolationVsPlatformView 是本次接入的**核心验收**：
// 同一批数据，租户身份只看本租户，平台身份看全部。
//
// 这条测试同时是 bald 侧 platform_identity_test.go 的**集成层对照**——
// 单元层锁定 shouldIsolate 的语义，本用例锁定它在真实查询链（HTTP → biz →
// ListWithPaging → translate → shouldIsolate）上确实生效。
func TestAudit_TenantIsolationVsPlatformView(t *testing.T) {
	base := startAuditREST(t)

	// 造两条审计：分属两个不同租户（唯一 subject 便于过滤）。
	const (
		tenantA = "pv-t-a"
		tenantB = "pv-t-b"
	)
	seedAuditForTenant(t, tenantA, "pv-subject-a")
	seedAuditForTenant(t, tenantB, "pv-subject-b")

	// 1) 租户 A 身份：只能看到本租户（隔离生效）。
	//
	// **subject 必须用真实种子用户（u-admin）**：casbin 的 g 行（subject→角色）
	// 装载时从 User.Roles 生成（bootstrap.go:607），虚构 subject 无角色 → 403。
	// 平台标记与租户值由 token 承载，故此处仍能模拟「租户 A 的 admin」。
	tokA := tenantToken(t, "admin", "u-admin", "admin", tenantA)
	resA := listAudit(t, base, tokA, "?subject=pv-subject-b")
	if len(resA.Items) != 0 {
		t.Fatalf("租户 A 查租户 B 的记录应被隔离（0 条），got %d", len(resA.Items))
	}
	// 本租户自己的记录可见。
	resAOwn := listAudit(t, base, tokA, "?subject=pv-subject-a")
	if len(resAOwn.Items) != 1 {
		t.Fatalf("租户 A 查本租户记录应命中 1 条，got %d", len(resAOwn.Items))
	}

	// 2) 平台身份（Platform=true）：跨租户可见——能看到 B 的记录。
	tokP := platformToken(t, "admin", "u-admin", tenantA)
	resP := listAudit(t, base, tokP, "?subject=pv-subject-b")
	if len(resP.Items) != 1 {
		t.Fatalf("平台身份应跨租户看到 B 的记录（1 条），got %d", len(resP.Items))
	}
	if resP.Items[0].GetTenantId() != tenantB {
		t.Fatalf("平台身份查到的记录租户应为 %s，got %s", tenantB, resP.Items[0].GetTenantId())
	}
}

// TestAudit_PlatformFlagDefaultsFalse 锁定 fail-closed：未置 Platform 的
// 令牌（普通租户身份）不获得跨租户能力——即使 TenantID 为空。
//
// 这条测试守护的是「平台豁免必须来自显式声明」——若实现改成
// 「TenantID 为空即放行」，空租户的匿名/异常令牌会 fail-open。
func TestAudit_PlatformFlagDefaultsFalse(t *testing.T) {
	// 直接验证 claims 层：Platform 零值为 false。
	claims := authn.AuthClaims{Subject: "u-x", TenantID: ""}
	if claims.Platform {
		t.Fatal("AuthClaims.Platform 零值必须为 false（fail-closed）")
	}
	// 空租户 + 无平台标记：ctx 不得被判为平台身份。
	ctx := authn.ContextWithAuthClaims(context.Background(), &claims)
	if contextx.PlatformFromContext(ctx) {
		t.Fatal("空租户且未声明 Platform 时，不得被判定为平台身份")
	}
	// 显式声明后才为 true。
	claims.Platform = true
	ctx2 := authn.ContextWithAuthClaims(context.Background(), &claims)
	if !contextx.PlatformFromContext(ctx2) {
		t.Fatal("显式 Platform=true 后应写入 ctx 平台标记")
	}
}
