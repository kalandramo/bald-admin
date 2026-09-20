// audit_wiring.go —— D14 修复：审计后端 provider 的惰性注册与绑定。
//
// ## D14 是什么
//
// 项目配置声明 `audit: { backends: "store,stream" }`，但按该配置启动**直接失败**：
//
//	Error: appkit: build audit: appkit: audit provider "store" not registered
//
// 根因是**两层**：
//
//  1. provider 从未注册（全仓搜 `storecontract`/`NewStoreProvider` 零命中）；
//  2. **时序矛盾**：`WithAuditRegistry` 是 BootstrapOption（须在
//     `FromBootstrap` 前提供），而 provider 需要 `*gorm.DB`——但 DB 要到
//     `BeforeStart` 才就绪。且 appkit 内部执行序是
//     `buildAudit`（`bald/pkg/appkit/bootstrap.go:509`）**先于**
//     `buildDatabases`（`:519`）——审计构建时 DB 必然为 nil。
//
// ## 修复思路（不改 bald 框架）
//
// `AuditProvider` 是**工厂函数**，返回的 `audit.Auditor` 只在**运行期**
// 被 `Record` 调用——那时 DB 早已就绪。故用**惰性包装**：
// 构造期不碰 DB，运行期首次 `Record` 时才解析 `bootstrappkg.DB` 并建
// `StoreAuditor`（`sync.Once` 保证只初始化一次）。
//
// 这条路径成立的依据（均已核实）：
//   - `audit.Auditor` 是**单方法接口**（`Record(ctx, AuditEvent)`），
//     `bald/pkg/audit/audit.go`——包装成本极低；
//   - `auditstore.New(db)` **接受 nil db**：`Record` 内
//     `if a.db == nil { recordFallback; return }`
//     （`bald/contrib/audit-store/store.go:70` 注释：「db 为 nil 时 Record
//     全部走 fallback（旁路不 panic）」）——故即便 DB 始终不可用也不丢审计
//     （降级到 LoggerAuditor），**fail-open 而非静默丢弃**。
//
// ## 为何不动框架（称量）
//
// 更彻底的修法是让 `NewStoreProvider` 收 `func() *gorm.DB`（惰性取 DB），
// 但那要改 bald 的公开 API。本项目原则是「不改 bald 框架本身，缺陷只记录」，
// 故选择应用层包装——代价是多一层间接，收益是零框架改动、零兼容风险。
//
// ## stream 后端为何不在本波修
//
// `streamcontract.NewStreamProvider(rdb)` 内部 `auditstream.New(rdb, ...)`
// 在 rdb 为 nil 时**直接返回 nil 并报错**（`contract.go:35-38`）——
// **stream 构造即需要 rdb**，无法惰性。且其 cleanup 是 `a.Close()`
// （停机 flush 尾批），惰性化会破坏该语义。
// 故本波只修 `store`（D14 主因，也是 dashboard 的审计数据源）；
// stream 保持现状，配置里如需启用须另行解决（见 D14 报告）。
package main

import (
	"context"
	"fmt"
	"sync"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/appkit"

	auditstore "github.com/kalandramo/bald/contrib/audit-store"
	storecontract "github.com/kalandramo/bald/contrib/audit-store/contract"
	gormpkg "gorm.io/gorm"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// lazyStoreAuditor 是 `store` 审计后端的惰性包装（D14 修复核心）。
//
// 构造期不解析 DB（此刻 DB 尚未就绪）；运行期首次 Record 时才取
// `bootstrappkg.DB` 并构造 `StoreAuditor`。DB 始终不可用时退化为
// fallback（LoggerAuditor），审计不丢。
type lazyStoreAuditor struct {
	// dbFn 惰性取 DB（返回 nil 表示 DB 尚不可用）。
	dbFn func() *gormpkg.DB
	// migrate 是否在首次解析时自动迁移审计表。
	migrate bool

	once sync.Once
	// inner 首次解析后缓存的实际 auditor（nil db 时仍会构造，
	// 由 StoreAuditor 自身走 fallback）。
	inner audit.Auditor
	// initErr 首次解析的迁移错误（仅记录日志，不阻断审计）。
	initErr error
}

// newLazyStoreAuditor 构造惰性落库审计器。
func newLazyStoreAuditor(dbFn func() *gormpkg.DB, migrate bool) *lazyStoreAuditor {
	return &lazyStoreAuditor{dbFn: dbFn, migrate: migrate}
}

// resolve 首次调用时解析 DB 并构造 StoreAuditor（幂等）。
func (l *lazyStoreAuditor) resolve() audit.Auditor {
	l.once.Do(func() {
		db := l.dbFn()
		if db == nil {
			// DB 仍不可用：构造 nil-db 的 StoreAuditor——其 Record 走
			// fallback（LoggerAuditor），**不 panic 也不丢痕迹**。
			l.inner = auditstore.New(nil)
			return
		}
		if l.migrate {
			if err := db.AutoMigrate(auditstore.DefaultModel()); err != nil {
				// 迁移失败不阻断：记录错误，仍构造 auditor（Record 会降级）。
				l.initErr = fmt.Errorf("audit-store: migrate default table: %w", err)
			}
		}
		l.inner = auditstore.New(db, auditstore.WithRecordMapper(securityaudit.RecordMapper))
	})
	return l.inner
}

// Record 实现 audit.Auditor：首次调用触发惰性解析。
func (l *lazyStoreAuditor) Record(ctx context.Context, ev audit.AuditEvent) {
	l.resolve().Record(ctx, ev)
}

// auditRegistry 构造并返回已注册 provider 的审计注册表（D14 修复入口）。
func auditRegistry() *appkit.AuditRegistry {
	reg := appkit.NewAuditRegistry()
	registerAuditProviders(reg)
	return reg
}

// registerAuditProviders 向 registry 注册审计后端 provider（D14 修复）。
//
// **必须在 `FromBootstrap` 之前调用**（registry 是 BootstrapOption），
// 但传入的是**惰性工厂**——真正的 DB 解析推迟到运行期首个审计事件。
//
// 只注册 `store`：`stream` 的 provider 构造即需要 redis 客户端
// （`auditstream.New(rdb)` 对 nil 直接报错），无法惰性化，见文件头说明。
func registerAuditProviders(reg *appkit.AuditRegistry) {
	reg.MustRegister(storecontract.TypeStore, func(_ context.Context,
		cfg *bootstrapv1.Audit) (audit.Auditor, func(context.Context) error, error) {
		// 惰性：此处**不**取 DB，只捕获取值函数。
		la := newLazyStoreAuditor(
			func() *gormpkg.DB { return bootstrappkg.DB },
			cfg.GetStore().GetMigrate(),
		)
		return la, nil, nil
	})
}
