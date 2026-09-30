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
	"errors"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/audit"

	auditgorm "github.com/kalandramo/bald/contrib/audit-gorm"
	auditstream "github.com/kalandramo/bald/contrib/audit-stream"
	streamcontract "github.com/kalandramo/bald/contrib/audit-stream/contract"
	goredis "github.com/redis/go-redis/v9"
	gormpkg "gorm.io/gorm"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
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
// 注册两个后端：
//   - `store`：落库（同步，gorm）。DB 取自 res.Database("sql")，回退包级 DB。
//   - `stream`：Redis Stream 异步后端。redis 客户端取自包级 RedisClient——
//     它由 CacheProvider（buildCaches，阶段 B 且早于 buildAudit）经
//     BuildRedisCache→UseRedisClient 填充；未配置 cache.redis 段时为 nil，
//     此时 provider 返回错误（fail-fast：配置声明了 stream 却无 Redis 可用，
//     属配置意图无法兑现，比静默降级可操作）。
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

	// stream 后端：**必须闭包内取** redis 客户端。
	//
	// 为何不用 streamcontract.NewStreamProvider(rdb)：它在 auditRegistry()
	// 调用时即求值 rdb（构造期，早于阶段 B 的 buildCaches），会把 nil 固化进
	// 闭包——实测表现为 `audit-stream: nil redis client`。与 store 分支同理：
	// 依赖必须在**消费时点**（阶段 B 的 buildAudit）读取，而非注册时点。
	reg.MustRegister(streamcontract.TypeStream, func(_ context.Context,
		cfg *bootstrapv1.Audit, res *appkit.AppKit) (audit.Auditor, func(context.Context) error, error) {
		rdb := resolveAuditRedis(res)
		if rdb == nil {
			return nil, nil, errors.New(
				"audit-stream: no redis client (configure cache.redis, or drop \"stream\" from audit.backends)")
		}
		s := cfg.GetStream()
		srv := auditstream.New(rdb,
			auditstream.WithStream(s.GetStream()),
			auditstream.WithBuffer(int(s.GetBuffer())),
		)
		if srv == nil {
			return nil, nil, errors.New("audit-stream: nil redis client")
		}
		// cleanup 即 StreamAuditor.Close（停后台 goroutine + drain 尾批缓冲）。
		return srv, func(context.Context) error { return srv.Close() }, nil
	})
}

// resolveAuditRedis 返回审计 stream 后端用的 Redis 客户端（消费时点求值）。
//
// 取自包级 RedisClient——CacheProvider 在阶段 B 的 buildCaches 经
// BuildRedisCache→UseRedisClient 填充，而 buildAudit 排在 buildCaches 之后，
// 故此处读到的已是真实实例。
//
// res.Cache("redis") 不是候选：cache.Cache 适配器**刻意不暴露底层 client**
// （见 cache/redis 包注释），而 stream 后端需原生 Redis 命令（XADD）。
//
// 注意：本函数是普通函数而非闭包字段——调用方（provider 闭包）在阶段 B
// 才调用它，故拿到的是彼时的值。若改成在注册时求值会固化 nil（已踩过）。
func resolveAuditRedis(_ *appkit.AppKit) goredis.UniversalClient {
	return bootstrappkg.RedisClient
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
