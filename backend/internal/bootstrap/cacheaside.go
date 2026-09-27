// cache.go 是 D1 迁移（2026-09-19）后的 Redis 缓存装配与键工具。
//
// 背景：原 contrib/cache-redis 已从 bald 删除（v0.8.1 起，见 bald 仓 commit
// 2bf5504「refactor(contrib): 删除 contrib/cache-redis 并同步 5 处现状文档」），
// 能力拆为两个独立 module：
//
//   - cache/redis：通用 KV 适配器（New(client, WithKeyPrefix)），只做
//     Get/Set/Delete 等原语，**不暴露底层 client**（其 Close 亦不关闭 client，
//     连接生命周期归调用方）；
//   - cache/loadable：读穿透组合器（New(backend, loader, WithTTL,
//     WithDegradeOnError)），未命中经 loader 加载并回填，并以 singleflight
//     合并同 key 并发 miss（旧实现无此保护，属能力增强）。
//
// 两者组合即旧 rediscache 的 Cache-Aside 语义，但 loader 形态不同——旧
// Get(ctx, key, loader) 是**请求期**传 loader（闭包捕获请求参数），新
// loadable.New 是**构造期**绑定，loader 签名为 func(ctx, key) ([]byte, error)。
// 参数化 key 场景（secret:<tenant>:<id> / dict:entries:<tenant>:<typeCode>）由
// loader 从 key 反解业务参数：租户段取自 ctx（与写键同源，避免键/上下文不一致），
// 业务键段经已知前缀裁剪并**校验前缀**（不匹配即报错，不静默取错数据）。
//
// 该形态限制（loadable 无请求期 loader 变体）已记录为框架反馈，见 Wave 0 交付报告。
package bootstrap

import (
	"context"
	"fmt"

	"github.com/kalandramo/bald/cache"
	rediscache "github.com/kalandramo/bald/cache/redis"
	goredis "github.com/redis/go-redis/v9"
)

// DefaultCacheTTL / CacheKey / CacheKeyPrefix / CutCacheKeyPrefix 已迁至
// internal/cachekit（Wave 3）——它们是纯字符串工具、无状态，留在 bootstrap 会
// 让 biz 为拼缓存键而反向依赖装配根（违反 G2 控制面单向依赖）。

// BuildRedisCache 按地址建连（含真实探活，避免假连接）并装配 KV 适配器。
// addr 为空返回 (nil, nil) 表示禁用态（调用方降级直连 store）。
//
// 连接同时记入 RedisClient，供审计流等需要原生 Redis 命令的消费方复用
// （cache/redis 适配器不暴露 client）。与旧 rediscache.New 的差异：旧实现在
// addr 为空时返回「非 nil 的禁用态 Cache」，新实现返回 nil——调用方 nil 判空
// 即降级，语义等价且更直白。
func BuildRedisCache(addr, password string, db int) (cache.Cache, error) {
	if addr == "" {
		return nil, nil
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: db})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping %s: %w", addr, err)
	}
	return UseRedisClient(rdb), nil
}

// UseRedisClient 以既有客户端装配 KV 适配器，并记录 RedisClient（审计流复用）。
func UseRedisClient(rdb goredis.UniversalClient) cache.Cache {
	if rdb == nil {
		return nil
	}
	RedisClient = rdb
	RedisCache = rediscache.New(rdb)
	return RedisCache
}
