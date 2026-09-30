// audit_wiring.go —— 审计后端 provider 注册（store 后端）。
//
// ## 历史（D14 与它的惰性包装）
//
// 项目配置声明 `audit: { backends: "store" }` 时曾启动失败：
//
//	Error: appkit: build audit: appkit: audit provider "store" not registered
//
// 根因有两层：provider 从未注册；且**装配时序倒置**——appkit 的 buildAudit
// 曾排在 buildDatabases **之前**，store 后端构造时 *gorm.DB 必为 nil。当时的
// 解法是应用层惰性包装（lazyStoreAuditor：构造期只捕获取值函数，请求期首次
// Record 才解析 DB）。
//
// ## 现在（2026-09-28 修订）
//
// 框架侧根治了两条：
//  1. buildAudit 移到 buildDatabases/buildCaches **之后**——资源先就绪；
//  2. AuditProvider 增 res 参数（*appkit.AppKit）——后端构造时可直接取
//     已装配的资源（res.Database("sql")）。
//
// 故惰性包装删除：本 provider 在构造期即取真实 *gorm.DB，与协调轨
// （reconcileAudit → securityaudit.NewStore）语义一致。
//
// ## 双轨说明（为何两处都有 store 后端构造）
//
//   - **契约轨**（本文件）：audit 段经 WithAuditRegistry 在阶段 B 装配；
//   - **协调轨**（audit.go 的 reconcileAudit）：audit.backends 期望态经
//     R1-2 协调器在 afterStart 后收敛（支持运行期热切换）。
//
// 二者都 SetAuditor 全局，协调轨在其后执行故最终生效；业务中间件经
// bundle.Audit(securityaudit.Global()) 动态转发，切换即时可见。契约轨在
// 「无协调器配置」或消费方未接协调轨时独立生效——故它自身不能有时序缺陷。
package app

import (
	"context"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/audit"

	auditgorm "github.com/kalandramo/bald/contrib/audit-gorm"
	gormpkg "gorm.io/gorm"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// auditRegistry 构造并返回已注册 provider 的审计注册表。
func auditRegistry() *appkit.AuditRegistry {
	reg := appkit.NewAuditRegistry()
	registerAuditProviders(reg)
	return reg
}

// registerAuditProviders 向 registry 注册审计后端 provider。
//
// **必须在 `FromBootstrap` 之前调用**（registry 是 BootstrapOption）。
// provider 在阶段 B 被调用时，res 已含装配完成的 database 客户端（框架保证
// buildAudit 排在 buildDatabases 之后），故直接取真实 DB。
//
// 只注册 `store`：`stream` 的 provider 构造即需要 redis 客户端，当前配置未
// 启用（见 configs/bald-admin.yaml 注释）；如需启用，按同款从
// res.Cache("redis") 取实例即可。
func registerAuditProviders(reg *appkit.AuditRegistry) {
	reg.MustRegister("store", func(_ context.Context,
		cfg *bootstrapv1.Audit, res *appkit.AppKit) (audit.Auditor, func(context.Context) error, error) {
		db := resolveAuditDB(res)
		if db == nil {
			// 无 DB 可用：返回 nil-db 的 StoreAuditor——其 Record 走 fallback
			// （LoggerAuditor），审计不丢、不 panic（fail-open）。
			// 正常路径不会走到（database 段存在时 res 必有 sql 客户端）。
			return securityaudit.NewStore(nil), nil, nil
		}
		if cfg.GetStore().GetMigrate() {
			if err := db.AutoMigrate(auditgorm.DefaultModel()); err != nil {
				return nil, nil, err
			}
		}
		return securityaudit.NewStore(db), nil, nil
	})
}

// resolveAuditDB 从已装配资源取审计落库用的 *gorm.DB。
//
// 优先契约装配的 sql 客户端（res.Database("sql")，框架阶段 B 就绪）；
// 回退包级 DB（InitBridges 自建路径，如无 database 段的独立测试）。
// 二者在生产指向同一实例（WireDatabase 把契约实例注入包级 DB）。
func resolveAuditDB(res *appkit.AppKit) *gormpkg.DB {
	if res != nil {
		if v, ok := res.Database("sql"); ok {
			if db, ok := v.(*gormpkg.DB); ok && db != nil {
				return db
			}
		}
	}
	return nil
}
