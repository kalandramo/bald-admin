// audit_wiring.go —— 审计段（audit 契约段）的**段驱动 Provider**：
// store/stream 后端经「用户声明自己的 audit 段 Provider」接入——段路径末者胜
// 覆盖 std 默认的 log-only Provider，因此本 Provider 须接管全部三种后端
// log/store/stream。

// ## 双轨说明（为何两处都有后端构造）
//
//   - **契约轨**（本文件）：audit 段在阶段 B（BeforeStart 装载链后）经本
//     Provider 装配，产物 audit.Auditor 由引擎全局注入（SetAuditor）并在
//     停机恢复装配前全局；
//   - **协调轨**（audit.go 的 reconcileAudit）：audit.backends 期望态经
//     R1-2 协调器在 AfterStart 前收敛（支持运行期热切换）。
//
// 二者都 SetAuditor 全局，协调轨在其后执行故最终生效；业务中间件经
// bundle.Audit(securityaudit.Global()) 动态转发，切换即时可见。契约轨在
// 「无协调器配置」或消费方未接协调轨时独立生效——故它自身不能有时序缺陷。

package app

import (
	"context"
	"errors"
	"fmt"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	auditgorm "github.com/kalandramo/bald/contrib/audit-gorm"
	auditstream "github.com/kalandramo/bald/contrib/audit-stream"
	storegorm "github.com/kalandramo/bald/contrib/store-gorm"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/audit"
	goredis "github.com/redis/go-redis/v9"
	gormpkg "gorm.io/gorm"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
)

// auditProvider 消费 audit 段：按 audit.backends 列表逐后端构造，多后端组装、
// 构造失败逆序回滚收敛在 pkg/audit.Compose（列表序即广播序）。后端构造时点
// 在阶段 B 的 audit 槽位——引擎保证其排在 database/cache 客户端之后（canonical
// 序），故 store 后端此刻可经 storegorm.SQL.Resource(app) 取真实连接。
type auditProvider struct{}

func (auditProvider) Segments() []string { return []string{"audit"} }

func (auditProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, auditProvider{}) }

// Plan 声明 audit 节点：阶段 B、审计优先级（依赖 database/cache 客户端就绪）；
// 停机用 ["*"] 排到最后——stream 尾批审计事件在指标/span flush 之后收集。
// audit 段变更可原地重建后端链（ReloadReconcile）：新链就绪后旧链在替换槽位
// 时被兑现，不泄漏。
func (auditProvider) Plan(context.Context, appkit.PlanScope) ([]appkit.Node, error) {
	return []appkit.Node{{
		Key:           "audit",
		Kind:          "audit",
		Segments:      []string{"audit"},
		Phase:         appkit.PhaseBootstrap,
		Priority:      appkit.PriorityAudit,
		TeardownAfter: []appkit.ResourceKey{"*"},
		EffectName:    "appkit:audit-restore",
		Reload:        appkit.ReloadReconcile,
	}}, nil
}

func (auditProvider) Build(ctx context.Context, s appkit.ApplyScope, _ appkit.Node) (any, appkit.Cleanup, error) {
	acfg := s.Config.GetAudit()
	if acfg == nil {
		return nil, nil, nil // 段缺省：不装配（引擎仅在段存在时调用，防御性）
	}
	backends := acfg.GetBackends()
	if len(backends) == 0 {
		return nil, nil, fmt.Errorf("audit: audit.backends is required when audit section is present (expect %q, %q or %q)",
			appkit.AuditTypeLog, appkit.AuditTypeStore, appkit.AuditTypeStream)
	}
	// store 后端的连接在循环外解析一次（多后端共用同一实例）。
	db := resolveAuditDB(s.App)
	backs := make([]audit.NamedBackend, 0, len(backends))
	for _, typ := range backends {
		switch typ {
		case appkit.AuditTypeLog:
			backs = append(backs, audit.NamedBackend{Name: typ, New: func(context.Context) (audit.Auditor, func(context.Context) error, error) {
				return securityaudit.New(), nil, nil
			}})
		case appkit.AuditTypeStore:
			backs = append(backs, audit.NamedBackend{Name: typ, New: func(context.Context) (audit.Auditor, func(context.Context) error, error) {
				return buildStoreAuditor(db, acfg)
			}})
		case appkit.AuditTypeStream:
			backs = append(backs, audit.NamedBackend{Name: typ, New: func(context.Context) (audit.Auditor, func(context.Context) error, error) {
				return buildStreamAuditor(acfg)
			}})
		default:
			return nil, nil, fmt.Errorf("audit: unknown backend %q (expect %q, %q or %q)",
				typ, appkit.AuditTypeLog, appkit.AuditTypeStore, appkit.AuditTypeStream)
		}
	}
	a2, cleanup, err := audit.Compose(ctx, backs...)
	if err != nil {
		return nil, nil, fmt.Errorf("audit: compose backends: %w", err)
	}
	return a2, cleanup, nil
}

// buildStoreAuditor 构造 store（落库）后端：db 为 nil 时返回 nil-db 的
// StoreAuditor——其 Record 走 fallback（LoggerAuditor），审计不丢、不 panic
// （fail-open）。正常路径不会 nil（database 段存在时资源面必有 sql 客户端）。
// migrate=true 时先 AutoMigrate（契约字段驱动，与 store-gorm 的建表职责
// 分离：审计表归属审计装配）。
func buildStoreAuditor(db *gormpkg.DB, acfg *bootstrapv1.Audit) (audit.Auditor, func(context.Context) error, error) {
	if db == nil {
		return securityaudit.NewStore(nil), nil, nil
	}
	if acfg.GetStore().GetMigrate() {
		if err := db.AutoMigrate(auditgorm.DefaultModel()); err != nil {
			return nil, nil, fmt.Errorf("audit: migrate store model: %w", err)
		}
	}
	return securityaudit.NewStore(db), nil, nil
}

// buildStreamAuditor 构造 stream（Redis Stream 异步）后端。
//
// redis 客户端取自包级 RedisClient——cacheRedisProvider（cache 槽位，排在
// audit 之前）经 BuildRedisCache→UseRedisClient 填充；未配置 cache.redis 段
// 时为 nil，此时返回错误（fail-fast：配置声明了 stream 却无 Redis 可用，属
// 配置意图无法兑现，比静默降级可操作——原契约轨语义保留）。
//
// Resource("cache.redis") 不是候选：cache.Cache 适配器**刻意不暴露底层
// client**（见 cache/redis 包注释），而 stream 后端需原生 Redis 命令（XADD）。
func buildStreamAuditor(acfg *bootstrapv1.Audit) (audit.Auditor, func(context.Context) error, error) {
	rdb := resolveAuditRedis()
	if rdb == nil {
		return nil, nil, errors.New(
			"audit-stream: no redis client (configure cache.redis, or drop \"stream\" from audit.backends)")
	}
	s := acfg.GetStream()
	srv := auditstream.New(rdb,
		auditstream.WithStream(s.GetStream()),
		auditstream.WithBuffer(int(s.GetBuffer())),
	)
	if srv == nil {
		return nil, nil, errors.New("audit-stream: nil redis client")
	}
	// cleanup 即 StreamAuditor.Close（停后台 goroutine + drain 尾批缓冲）。
	return srv, func(context.Context) error { return srv.Close() }, nil
}

// resolveAuditRedis 返回审计 stream 后端用的 Redis 客户端（消费时点求值）。
//
// 取自包级 RedisClient——cacheRedisProvider 在阶段 B 的 cache 槽位经
// BuildRedisCache→UseRedisClient 填充，而 audit 槽位排在 cache 之后，故此处
// 读到的已是真实实例。注意本函数是普通函数而非闭包字段——调用方（provider
// 闭包）在阶段 B 才调用它，故拿到的是彼时的值。若改成在声明期求值会固化 nil
// （已踩过）。
func resolveAuditRedis() goredis.UniversalClient {
	return bootstrappkg.RedisClient
}

// resolveAuditDB 从已装配资源取审计落库用的 *gorm.DB。
//
// 优先契约装配的 sql 客户端（storegorm.SQL.Resource(res)），
// 取不到返回 nil（buildStoreAuditor 走 fail-open
// 分支）。res 可为 nil（直测 Provider 时无 App）。
func resolveAuditDB(res *appkit.App) *gormpkg.DB {
	if db, ok := storegorm.SQL.Resource(res); ok && db != nil {
		return db
	}
	return nil
}
