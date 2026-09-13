// providers.go 是 U1（FromBootstrap 切换）的透传 provider 层：
// go-bald-admin 的 DB/Redis/MinIO 桥接是业务自管装配（openDB/resolveRedis
// 保留 env 优先级与降级语义），但契约段（database.sql / cache.redis /
// storage.minio）在 FromBootstrap 阶段 B 要求「段存在必有 Registry 消费者」
// （fail-fast，防「配置说开了没人实现」）。本文件把既有构造逻辑包成
// appkit 的 DatabaseProvider/CacheProvider/StorageProvider——
// 零框架改动、构造语义不变，连接生命周期（cleanup）上挂框架停机 Effect。
//
// 与 contrib/database/<engine>/contract 的官方 provider 的区别：官方路径
// 返回生态客户端（*gormcrud.Client 等），本业务桥接直接消费 *gorm.DB /
// *rediscache.Cache / *miniooss.Storage（stores/审计/文件模块的既有类型）。
// 两条路径都合法（「代码声明能力」），范例选择保留自有桥接。
package bootstrap

import (
	"context"

	"gorm.io/gorm"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	rediscache "github.com/kalandramo/bald/contrib/cache-redis"
	"github.com/kalandramo/bald/log"

	miniooss "github.com/kalandramo/bald/oss/minio"
)

// DatabaseProvider 是契约 database.sql 段的透传 provider：构造走 openDB
// （env BALD_ADMIN_DB_DSN > 契约段 > SQLite 内存库），返回 *gorm.DB。
// cleanup 关连接池（挂框架停机 Effect——此前 New 路径 DB 连接从不显式关，
// 此为生命周期增强）。
func DatabaseProvider(ctx context.Context, cfg *bootstrapv1.Database) (any, func(), error) {
	db, err := openDB(cfg.GetSql())
	if err != nil {
		return nil, nil, err
	}
	return db, func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	}, nil
}

// CacheProvider 是契约 cache.redis 段的透传 provider：构造走 resolveRedis
// 同款参数解析（addr/password/db）。Redis 不可达仅 warn 不阻断（审计流降级，
// 与 InitBridges 既有语义一致）——返回 nil 实例，消费侧按 nil 降级。
// rediscache.Cache 无 Close 方法（连接池随进程退出），cleanup 为 nil。
func CacheProvider(ctx context.Context, cfg *bootstrapv1.Cache) (any, func(), error) {
	rc := cfg.GetRedis()
	if rc == nil || rc.GetAddr() == "" {
		return nil, nil, nil // 段存在但空 addr：禁用态（与 resolveRedis 空返回同构）
	}
	c, err := rediscache.New(rc.GetAddr(),
		rediscache.WithPassword(rc.GetPassword()),
		rediscache.WithDB(int(rc.GetDb())),
	)
	if err != nil {
		log.Warn(ctx, "redis init skipped, audit stream disabled", "error", err.Error())
		return nil, nil, nil
	}
	return c, nil, nil
}

// StorageProvider 是契约 storage.minio 段的透传 provider：minio.New 仅本地
// 构造不联网，失败（SDK() 为 nil）warn + nil（文件模块降级，EnsureBucket
// 兜底），与 InitBridges 既有语义一致。无长连接需 cleanup（SDK 无 Close）。
func StorageProvider(ctx context.Context, cfg *bootstrapv1.Storage) (any, func(), error) {
	mc := cfg.GetMinio()
	if mc == nil || mc.GetEndpoint() == "" {
		return nil, nil, nil
	}
	st := miniooss.NewStorage(&miniooss.Config{
		Endpoint:  mc.GetEndpoint(),
		AccessKey: mc.GetAccessKey(),
		SecretKey: mc.GetSecretKey(),
		Token:     mc.GetToken(),
		UseSsl:    mc.GetUseSsl(),
	})
	if st == nil || st.SDK() == nil {
		log.Warn(ctx, "minio init failed, file module degraded", "endpoint", mc.GetEndpoint())
		return nil, nil, nil
	}
	log.Info(ctx, "minio storage constructed", "endpoint", mc.GetEndpoint())
	return st, nil, nil
}

// ---------------------------------------------------------------------------
// 注入面：main 的 WithBeforeStart 钩子（阶段 B 之后）经 AppKit 取契约装配
// 结果注入桥接变量；未注入时 InitBridges 回退自建（既有测试/独立调用零改动）。
// ---------------------------------------------------------------------------

// WireDatabase 注入契约装配的应用主库（app.Database("sql") 的结果）。
// 断言收敛在桥接包：main 侧不 import gorm。nil 透传为 no-op。
func WireDatabase(v any) {
	if db, ok := v.(*gorm.DB); ok && db != nil {
		DB = db
	}
}

// WireCache 注入契约装配的 Redis 实例（app.Cache("redis") 的结果）。
func WireCache(v any) {
	if c, ok := v.(*rediscache.Cache); ok && c != nil {
		RedisCache = c
	}
}

// WireStorage 注入契约装配的 MinIO 实例（app.Storage("minio") 的结果）。
func WireStorage(v any) {
	if st, ok := v.(*miniooss.Storage); ok && st != nil {
		MinioStorage = st
	}
}
