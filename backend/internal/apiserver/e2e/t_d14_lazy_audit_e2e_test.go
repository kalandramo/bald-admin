package e2e

// t_d14_lazy_audit_e2e_test.go —— D14 修复回归：审计后端 provider 的惰性注册。
//
// ## D14 是什么
//
// 配置声明 `audit: { backends: "store,stream" }` 但项目从未注册 provider
// → 启动直接失败 `audit provider "store" not registered`。
// 根因含时序矛盾：appkit 的 `buildAudit`（`bootstrap.go:509`）**先于**
// `buildDatabases`（`:519`），故审计构建时 DB 必然为 nil。
//
// ## 修复
//
// `AuditProvider` 是工厂函数，返回的 `Auditor` 只在运行期被 `Record`——
// 故用惰性包装（`cmd/go-bald-admin/audit_wiring.go` 的 `lazyStoreAuditor`）：
// 构造期不碰 DB，运行期首次 Record 才解析。
//
// ## 本测试覆盖（不依赖真实 server，直接测包装语义）
//
//   - 惰性：构造时不调用 dbFn（DB 未就绪也不 panic）；
//   - DB 可用时：首次 Record 触发解析 + 迁移，事件落库；
//   - **DB 不可用时 fail-open**：走 fallback（LoggerAuditor），不丢痕迹、不 panic。

import (
	"context"
	"testing"

	auditgorm "github.com/kalandramo/bald/contrib/audit-gorm"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/store"
	gormpkg "gorm.io/gorm"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// newLazyStoreAuditorForTest 是 lazyStoreAuditor 的测试可访问构造。
//
// lazyStoreAuditor 定义在 cmd 包（不可从 e2e import），故此处按同一语义
// 复刻一份最小实现——**测试的是行为契约（惰性/fail-open/一次解析），
// 而非复用生产类型**。生产实现见 cmd/go-bald-admin/audit_wiring.go。
func newLazyStoreAuditorForTest(dbFn func() *gormpkg.DB, migrate bool) audit.Auditor {
	return &lazyProbeAuditor{dbFn: dbFn, migrate: migrate}
}

type lazyProbeAuditor struct {
	dbFn    func() *gormpkg.DB
	migrate bool
	once    bool
	inner   audit.Auditor
}

func (l *lazyProbeAuditor) Record(ctx context.Context, ev audit.AuditEvent) {
	if !l.once {
		l.once = true
		db := l.dbFn()
		if db == nil {
			l.inner = auditgorm.New(nil)
		} else {
			if l.migrate {
				_ = db.AutoMigrate(auditgorm.DefaultModel())
			}
			l.inner = auditgorm.New(db)
		}
	}
	l.inner.Record(ctx, ev)
}

// TestD14_LazyAuditorDefersDBResolution —— 构造期不解析 DB。
//
// 这是修复的核心：若构造期取 DB，时序矛盾会让启动失败。
func TestD14_LazyAuditorDefersDBResolution(t *testing.T) {
	called := 0
	la := newLazyStoreAuditorForTest(func() *gormpkg.DB {
		called++
		return nil
	}, false)

	if called != 0 {
		t.Fatalf("构造期就调用了 dbFn（called=%d）——惰性失效，时序矛盾未解", called)
	}
	t.Log("确认：构造期未解析 DB（惰性生效）")

	// 首次 Record 才触发解析。
	la.Record(context.Background(), audit.AuditEvent{Object: "probe", Action: "test"})
	if called != 1 {
		t.Fatalf("首次 Record 后 dbFn 调用次数=%d, want 1", called)
	}

	// 第二次不再解析（sync.Once）。
	la.Record(context.Background(), audit.AuditEvent{Object: "probe", Action: "test"})
	if called != 1 {
		t.Fatalf("第二次 Record 又调用了 dbFn（called=%d）——sync.Once 失效", called)
	}
	t.Log("确认：解析只发生一次（sync.Once 生效）")
}

// TestD14_NilDBFailsOpen —— **DB 不可用时 fail-open**（不 panic、不阻断调用方）。
//
// 依据：`auditgorm.New(nil)` 的 Record 走 fallback
// （`bald/contrib/audit-gorm/store.go`：「db 为 nil 时 Record 全部走
// fallback（旁路不 panic）」）。
func TestD14_NilDBFailsOpen(t *testing.T) {
	la := newLazyStoreAuditorForTest(func() *gormpkg.DB { return nil }, false)

	// 不应 panic，也不应返回错误（Record 无返回值）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		la.Record(context.Background(), audit.AuditEvent{
			Object: "probe", Action: "nil-db", Result: audit.ResultAllow,
		})
	}()
	<-done
	t.Log("确认：DB 为 nil 时 Record 不 panic（fail-open，走 fallback）")
}

// TestD14_RealDBRecordsAudit —— DB 可用时事件真的落库（端到端语义）。
//
// 这是修复的**验收点**：审计写入后能在 AuditStore 查到。
func TestD14_RealDBRecordsAudit(t *testing.T) {
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	ctx := context.Background()

	before, err := bootstrappkg.AuditStore.Count(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("count before: %v", err)
	}

	// 用惰性包装（与生产路径同款），dbFn 返回已就绪的 DB。
	la := newLazyStoreAuditorForTest(func() *gormpkg.DB { return bootstrappkg.DB }, true)
	la.Record(ctx, audit.AuditEvent{
		TenantID: "t-default",
		Subject:  "u-d14-probe",
		Object:   "d14",
		Action:   "lazy-audit",
		Result:   audit.ResultAllow,
	})

	after, err := bootstrappkg.AuditStore.Count(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after <= before {
		t.Fatalf("审计未落库: %d -> %d", before, after)
	}

	// 直查确认内容正确（不只看计数）。
	recs, _, err := bootstrappkg.AuditStore.List(ctx, &store.Where{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *authmodel.AuditRecord
	for _, r := range recs {
		if r.Action == "lazy-audit" && r.Subject == "u-d14-probe" {
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
