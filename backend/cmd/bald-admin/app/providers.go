package app

// providers.go —— **业务自持语义**的段驱动 Provider 集合（各段由 Provider
// 声明，经 appkit.MustServe 的段驱动装配引擎消费；范本 contrib/store-gorm 的
// AppProvider 与 _example/bald/main.go）。
//
// 通用后端已全部由官方 Provider 承担，在 assembly.go 的 options 清单里并列
// 声明：database.sql→store-gorm、storage.minio/s3、registry.nacos、
// config→config-nacos、tracer/metrics→observability-otlp、logger→std。
//
// 本文件只放官方 Provider 覆盖不了的业务语义：
//   - cache.redis：Redis 不可达仅告警（审计流降级），且把底层原生 client
//     记入 RedisClient 供 TokenStore/验证码/MFA/审计流/cachemonitor 复用；
//   - app：把 ApplyScope.App 绑定给业务侧句柄。audit 段见 audit_wiring.go。
//
// App 绑定：Serve 路径下 App 由框架内部构造，业务声明期拿不到实例——阶段 B
// Provider 的 ApplyScope.App 是业务侧唯一取用点（本仓的管理面 handler 经
// appRef 请求期取 App、BeforeStart 经 appRef 读 Settings，都依赖此绑定）。
// 绑定由 appBindingProvider 承担：契约 app 段被 bconf.NewBootstrap 预填（恒
// 存在），且 app 顶层段缺省相位即阶段 B——故它**无条件**触发，不依赖任何
// 业务段是否配置。

import (
	"context"
	"fmt"

	"github.com/kalandramo/bald/cache"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/appkit"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// configNamespace 是本进程的配置命名空间常量——决定 `--config` 缺省路径
// `configs/{configNamespace}.yaml`（经 appkit.ConfigFile 声明，Serve 内部的
// parseConfigFlag 以它为默认值）。
//
// env 前缀（`BALD_ADMIN_*`）与多环境文件名由**契约 app.name 派生**
// （两遍合并：文件层先解析 app.name，再按它挂 env 层）——configs/bald-admin.yaml
// 的 app.name: bald-admin 即其来源，契约无 app.name 时启动 fail-fast 且报错
// 可行动。
const configNamespace = "bald-admin"

// configFileDefault 本地配置文件缺省路径，由 configNamespace 派生而非独立字面量。
//
// 必须显式声明路径（不能传空串让框架自动发现）——两者行为不同：
//   - 显式路径：文件缺失 → fail-fast（`open ...: cannot find the path`），
//     启动即暴露，且报错直指缺失文件；
//   - 空串：框架 discoverLocalFile 找不到文件时静默跳过文件层，最终以
//     `no config source configured` 报错——真实原因（文件不在）被掩盖。
//
// 本服务的配置携带 DB/Redis 凭据，宁可启动即失败也不要半配置运行。
const configFileDefault = "configs/" + configNamespace + ".yaml"

// ---------------------------------------------------------------------------
// cache 段：业务桥接（env 优先级 / 降级 / RedisClient 记录）
// ---------------------------------------------------------------------------

// cacheRedisProvider 消费 cache.redis 段，保留业务桥接的既有构造语义：
//   - 官方 cache/redis contract.AppProvider 在段存在时 validate/dial/Ping
//     **fail-fast**；本服务要求 Redis 不可达仅降级（审计流禁用），且
//     BuildRedisCache 需把底层原生 client 记入 RedisClient 供
//     TokenStore/验证码/MFA/审计流/cachemonitor 复用——官方 Provider 自行
//     持有连接，无法在同一次构造里完成记录（取舍：保留业务 Provider，
//     接缝与 audit 段同款「用户声明自己的 Provider」）；
//   - Redis 不可达仅 warn 不阻断（审计流降级），与 InitBridges 既有语义一致
//     ——返回空产物，消费侧按缺失降级；
//   - BuildRedisCache 经 UseRedisClient 把底层 client 记入 RedisClient
//     （审计流等消费方复用）；连接生命周期归本进程（适配器 Close 不关
//     client，无 cleanup）。
type cacheRedisProvider struct{}

// CacheRedis 是本业务 cache.redis Provider 的类型化句柄。产物是 cache.Cache
// 适配器（与官方 contract 的 *rediscache.Cache 不同型——本业务消费方还依赖
// BuildRedisCache 记录的 RedisClient，见上方取舍说明）。
var CacheRedis = appkit.NewKey[cache.Cache]("cache.redis")

func (cacheRedisProvider) Segments() []string { return []string{"cache.redis"} }

func (cacheRedisProvider) Apply(d *appkit.Decl) {
	d.Providers = append(d.Providers, cacheRedisProvider{})
}

// Plan 声明 cache 段节点：阶段 B、cache 优先级，停机效果名沿用旧 canonical 名。
// 客户端地址变更需重启（ReloadRestart）：连接池/底层 client 不原地重建。
func (cacheRedisProvider) Plan(context.Context, appkit.PlanScope) ([]appkit.Node, error) {
	return []appkit.Node{{
		Key:        "cache.redis",
		Kind:       "cache",
		Segments:   []string{"cache.redis"},
		Phase:      appkit.PhaseBootstrap,
		Priority:   appkit.PriorityCache,
		EffectName: "appkit:cache-clients",
		Reload:     appkit.ReloadRestart,
	}}, nil
}

func (cacheRedisProvider) Build(ctx context.Context, s appkit.ApplyScope, _ appkit.Node) (any, appkit.Cleanup, error) {
	rc := s.Config.GetCache().GetRedis()
	if rc == nil || rc.GetAddr() == "" {
		return map[string]any{}, nil, nil // 段存在但空 addr：禁用态（与 resolveRedis 空返回同构）
	}
	c, err := bootstrappkg.BuildRedisCache(rc.GetAddr(), rc.GetPassword(), int(rc.GetDb()))
	if err != nil {
		log.Warn(ctx, "redis init skipped, audit stream disabled", "error", err.Error())
		return map[string]any{}, nil, nil
	}
	return map[string]any{"redis": c}, nil, nil
}

// ---------------------------------------------------------------------------
// app 段：App 实例绑定（无条件触发的阶段 B Provider）
// ---------------------------------------------------------------------------

// appBindingProvider 把 ApplyScope.App 绑定给业务侧句柄（appRef）。契约 app
// 段被 bconf.NewBootstrap 预填、恒存在，app 顶层段缺省相位即阶段 B（appkit.
// phaseByTop）——故本 Provider 在每次 Serve 装配中**无条件**触发，App 绑定不
// 依赖任何业务段是否配置。无产物、无清理（纯副作用绑定）。
type appBindingProvider struct {
	// bindApp 由装配方提供（appRef.set）；nil 在 Build 时显式失败（装配缺陷）。
	bindApp func(*appkit.App)
}

func (appBindingProvider) Segments() []string { return []string{"app"} }

func (p appBindingProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, p) }

// Plan 声明 App 绑定节点：app 段（框架恒消费的顶层段）、阶段 B、优先级 0
// ——绑定排在任何资源 Provider 之前，后续 Provider/BeforeStart 都拿得到 App。
// Key 用 "app.binding" 避开框架合成的 app 元数据节点（同 Key 即冲突）。
func (appBindingProvider) Plan(context.Context, appkit.PlanScope) ([]appkit.Node, error) {
	return []appkit.Node{{
		Key:      "app.binding",
		Kind:     "app",
		Segments: []string{"app"},
		Phase:    appkit.PhaseBootstrap,
		Priority: 0,
	}}, nil
}

func (p appBindingProvider) Build(ctx context.Context, s appkit.ApplyScope, _ appkit.Node) (any, appkit.Cleanup, error) {
	if p.bindApp == nil {
		return nil, nil, fmt.Errorf("appBindingProvider: bindApp not wired (assembly bug)")
	}
	p.bindApp(s.App)
	return nil, nil, nil
}
