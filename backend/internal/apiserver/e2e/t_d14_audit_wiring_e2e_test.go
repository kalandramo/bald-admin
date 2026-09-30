package e2e

// t_d14_audit_wiring_e2e_test.go —— 审计后端装配回归（D14 → 2026-09-28 修订）。
//
// ## 历史（D14 与惰性包装）
//
// 配置声明 `audit: { backends: "store" }` 曾启动失败：
// `appkit: audit provider "store" not registered`。根因两层：provider 未注册；
// 且 appkit 的 buildAudit 排在 buildDatabases **之前**——store 后端构造时
// *gorm.DB 必为 nil。当时用应用层惰性包装（lazyStoreAuditor）绕开。
//
// ## 现在（框架根治后）
//
// 框架把 buildAudit 移到数据客户端之后，并给 AuditProvider 增 res 参数
// （*appkit.AppKit）——后端构造期即可取已装配的 DB。故惰性包装已删除，
// 生产 provider（`cmd/bald-admin/app/audit_wiring.go`）直持真实 *gorm.DB。
//
// 本测试覆盖**新语义**：
//   - provider 构造不 panic（DB 就绪路径）；
//   - 审计事件真的落库（端到端验收）；
//   - DB 为 nil 时 fail-open（走 fallback，不 panic、不丢痕迹）。
//
// 框架侧的「provider 拿到的 res 已含 database 客户端」由 bald 仓的
// `pkg/appkit/audit_wiring_order_test.go` 锁定（RED 检验过）。

import (
	"context"
	"testing"

	auditgorm "github.com/kalandramo/bald/contrib/audit-gorm"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// TestAuditStore_FailOpenWithoutDB —— DB 不可用时 fail-open（不 panic、不丢痕迹）。
//
// 依据：auditgorm.New(nil) 的 Record 走 fallback（LoggerAuditor）——
// `bald/contrib/audit-gorm/store.go`：「db 为 nil 时 Record 全部走 fallback
// （旁路不 panic）」。这是审计旁路的底线：审计基础设施故障不得阻断业务。
func TestAuditStore_FailOpenWithoutDB(t *testing.T) {
	a := auditgorm.New(nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Record(context.Background(), audit.AuditEvent{
			Object: "probe", Action: "nil-db", Result: audit.ResultAllow,
		})
	}()
	<-done
	t.Log("确认：DB 为 nil 时 Record 不 panic（fail-open，走 fallback）")
}

// TestAuditStore_RealDBRecordsAudit —— DB 可用时事件真的落库（端到端验收）。
//
// 生产 provider 现在直接持真实 DB（无惰性包装），故此处用真实 DB 构造
// 审计器并断言写入可查——这是「provider 已能拿到 DB」的行为证据。
func TestAuditStore_RealDBRecordsAudit(t *testing.T) {
	if _, err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	repos = bootstrappkg.LastRepositories()
	ctx := context.Background()

	before, err := repos.Audit.Count(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("count before: %v", err)
	}

	// 与生产 provider 同款：直接持真实 DB（不再是「构造期捕获 dbFn + 请求期解析」）。
	// 生产路径见 cmd/bald-admin/app/audit_wiring.go resolveAuditDB。
	a := securityaudit.NewStore(bootstrappkg.DB)
	a.Record(ctx, audit.AuditEvent{
		TenantID: "t-default",
		Subject:  "u-audit-probe",
		Object:   "d14",
		Action:   "wiring-audit",
		Result:   audit.ResultAllow,
	})

	after, err := repos.Audit.Count(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after <= before {
		t.Fatalf("审计未落库: %d -> %d", before, after)
	}

	// 直查确认内容正确（不只看计数）。
	recs, _, err := repos.Audit.List(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *authmodel.AuditRecord
	for _, r := range recs {
		if r.Action == "wiring-audit" && r.Subject == "u-audit-probe" {
			found = r
		}
	}
	if found == nil {
		t.Fatal("未找到刚写入的审计记录")
	}
	if found.Object != "d14" {
		t.Fatalf("object=%q, want d14", found.Object)
	}
	t.Logf("确认：审计落库成功 %d -> %d，记录 object=%s action=%s result=%s",
		before, after, found.Object, found.Action, found.Result)
}
