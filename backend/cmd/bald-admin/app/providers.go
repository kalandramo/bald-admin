package app

// providers.go —— 批 2 装配形态的**段驱动 Provider**集合（原 registries.go 的
// 13 张 New+MustRegister+WithXRegistry 注册表整体删除，各段改由 Provider 声明，
// 经 appkit.MustServe 的段驱动装配引擎消费；范本 contrib/store-gorm 的
// AppProvider 与 _example/bald/main.go）。
//
// 各 Provider 的分工：
//   - 官方后端直接用 contract.AppProvider()（database.sql→store-gorm、
//     storage.minio/s3、registry.nacos、server.{asynq,cron,gateway,sse}），
//     在 assembly.go 的 options 清单里并列声明；
//   - 本文件只放**业务自持语义**的段 Provider：config（nacos 远程层）、
//     cache.redis（env/降级/RedisClient 记录）、tracer、metrics
//     （observability-otlp 函数契约缝 + 暴露端自持）。audit 段见 audit_wiring.go。
//
// App 绑定：Serve 路径下 App 由框架内部构造，业务声明期拿不到实例——阶段 B
// Provider 的 BuildScope.App 是业务侧唯一取用点（本仓的管理面 handler 经
// appRef 请求期取 App、BeforeStart 经 appRef 读 Settings，都依赖此绑定）。
// 绑定由 appBindingProvider 承担：契约 app 段被 bconf.NewBootstrap 预填（恒
// 存在），且 app 顶层段缺省相位即阶段 B——故它**无条件**触发，不依赖任何
// 业务段是否配置。

import (
	"context"
	"fmt"
	"net/http"

	baldbootstrap "github.com/kalandramo/bald/bootstrap"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	otlpcontract "github.com/kalandramo/bald/contrib/observability-otlp/contract"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/appkit"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// configNamespace 是本进程的**配置命名空间**——唯一事实来源。它同时决定两件事：
//
//  1. `--config` 缺省路径：`configs/{namespace}.yaml`（经 appkit.ConfigFile 声明，
//     Serve 内部的 parseConfigFlag 以它为默认值——原 preloadBootstrap 手写扫描
//     已随批 2 内化删除，默认路径语义等价）；
//  2. env 前缀（`BALD_ADMIN_*`）与多环境文件名（`bald-admin-{env}.yaml`）
//     ——经 appkit.ConfigNamespace 声明（见 assembly.go）。
//
// 为什么必须是代码层常量：Serve 路径下若取自契约 app.name 会构成自指（配置
// 文件写 app.name 会让全部 BALD_ADMIN_* env 覆盖静默失效）。
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
// config 段：配置源层（构造期产层）
// ---------------------------------------------------------------------------

// configSourceProvider 消费契约 config 段（构造期产层）。远程 nacos 源经
// bootstrap.NacosProvider 建层（层语义：Watch=true 参与热更新）；本地文件层
// 由 appkit.ConfigFile 内建（原 ConfigRegistry 的 file provider 与其读同一路径、
// 内容一致，批 2 起不再重复注册）。层优先级不变：本地文件 > 契约源层（nacos）
// ——「远程只补本地未定义的键」。
//
// 段缺失语义：config 段 Provider 被**无条件构建**（源可独立于 config 段存在，
// 见 appkit.consumedSegments）——未配置 nacos 时 NacosProvider 返回 nil 层，本
// Provider 返回空层列表（合法，不报错；原 ConfigRegistry「至少一个 layer」的
// 启动检查由内建 ConfigFile 层承接）。
type configSourceProvider struct{}

func (configSourceProvider) Segments() []string { return []string{"config"} }

// Apply 把自身追加进声明（Provider 即 Option，可直接作为 MustServe 变参项）。
func (configSourceProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, configSourceProvider{}) }

func (configSourceProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	l, closer, err := baldbootstrap.NacosProvider()(ctx, s.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("config source nacos: %w", err)
	}
	var layers []baldconfig.Layer
	if l != nil {
		layers = append(layers, *l)
	}
	var cleanup appkit.Cleanup
	if closer != nil {
		cleanup = func(context.Context) error { closer(); return nil }
	}
	return layers, cleanup, nil
}

// ---------------------------------------------------------------------------
// cache 段：业务桥接（env 优先级 / 降级 / RedisClient 记录）
// ---------------------------------------------------------------------------

// cacheRedisProvider 消费 cache.redis 段，保留业务桥接的既有构造语义：
//   - 官方 cache/redis contract.AppProvider 在段存在时 validate/dial/Ping
//     **fail-fast**，且产物 *rediscache.Cache **不暴露底层 client**——本业务的
//     TokenStore/验证码/MFA/审计流/cachemonitor 都需要原生 go-redis client，
//     官方 Provider 形态不满足（任务书冲突项，取舍：保留业务 Provider，
//     注册表已删、接缝与 audit 段同款「用户声明自己的 Provider」）；
//   - Redis 不可达仅 warn 不阻断（审计流降级），与 InitBridges 既有语义一致
//     ——返回空产物，消费侧按缺失降级；
//   - BuildRedisCache 经 UseRedisClient 把底层 client 记入 RedisClient
//     （审计流等消费方复用）；连接生命周期归本进程（适配器 Close 不关
//     client，无 cleanup）。
type cacheRedisProvider struct{}

func (cacheRedisProvider) Segments() []string { return []string{"cache.redis"} }

func (cacheRedisProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, cacheRedisProvider{}) }

func (cacheRedisProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
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
// tracer / metrics 段：observability-otlp 函数契约缝 + 暴露端自持
// ---------------------------------------------------------------------------

// tracerProvider 消费 tracer 段：按 tracer.type 分派到 observability-otlp 的
// 既有构造函数（appkit.TracerProvider 函数契约缝，批 2 保留）。原
// TracerRegistry 的注册表形态删除，type 分派（当前唯一后端 otlp）收敛到本
// Provider；未注册 type fail-fast 语义保留。产物为 nil（shutdown 由 cleanup
// 槽位承载，引擎按 "appkit:tracer-shutdown" Effect 逆序回放最后执行）。
type tracerProvider struct{}

func (tracerProvider) Segments() []string { return []string{"tracer"} }

func (tracerProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, tracerProvider{}) }

func (tracerProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	tc := s.Config.GetTracer()
	if tc == nil {
		return nil, nil, nil // 段缺省：不装配
	}
	var shutdown func(context.Context) error
	switch tc.GetType() {
	case otlpcontract.TracerType:
		sd, err := otlpcontract.NewTracerProvider(configNamespace)(ctx, tc)
		if err != nil {
			return nil, nil, fmt.Errorf("tracer: build otlp backend: %w", err)
		}
		shutdown = sd
	default:
		return nil, nil, fmt.Errorf("tracer: unknown tracer.type %q (registered: %q)",
			tc.GetType(), otlpcontract.TracerType)
	}
	return nil, func(ctx context.Context) error {
		if shutdown == nil {
			return nil
		}
		return shutdown(ctx)
	}, nil
}

// metricsProvider 消费 metrics 段：metrics.type **单选**（原 MetricsRegistry
// 注册两个 key 供其选择，注册表形态删除后分派收敛到本 Provider）：
//   - "prometheus"：仅本地抓取端点；
//   - "otlp"：本地抓取 + OTLP 周期推送（两个 reader 挂**同一 MeterProvider**，
//     同源双出口——OTLP 推送不依赖 :9091 端点是否监听）。
//
// 抓取端 http.Server 由本 Provider 自持起停（appkit.StartMetricsServer +
// cleanup 里 Shutdown；原 buildObservability 的暴露端装配随注册表删除迁入）。
// 停机序沿用原语义：先停暴露端、再 flush 直推通道（收集尾批指标），由引擎
// "appkit:metrics-server" Effect 逆序回放最后执行。
type metricsProvider struct{}

func (metricsProvider) Segments() []string { return []string{"metrics"} }

func (metricsProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, metricsProvider{}) }

func (metricsProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	mc := s.Config.GetMetrics()
	if mc == nil {
		return nil, nil, nil // 段缺省：不装配
	}
	var (
		handler http.Handler
		flush   func(context.Context) error
	)
	switch mc.GetType() {
	case otlpcontract.TypePrometheus:
		h, f, err := otlpcontract.NewPrometheusProvider(configNamespace)(ctx, mc)
		if err != nil {
			return nil, nil, fmt.Errorf("metrics: build prometheus backend: %w", err)
		}
		handler, flush = h, f
	case otlpcontract.TypeOTLP:
		h, f, err := otlpcontract.NewOTLPProvider(configNamespace)(ctx, mc)
		if err != nil {
			return nil, nil, fmt.Errorf("metrics: build otlp backend: %w", err)
		}
		handler, flush = h, f
	default:
		return nil, nil, fmt.Errorf("metrics: unknown metrics.type %q (registered: %q, %q)",
			mc.GetType(), otlpcontract.TypePrometheus, otlpcontract.TypeOTLP)
	}
	srv := appkit.StartMetricsServer(mc.GetPrometheus().GetAddr(), mc.GetPrometheus().GetPath(), handler)
	return nil, func(ctx context.Context) error {
		if srv != nil {
			if err := srv.Shutdown(ctx); err != nil {
				log.Error(ctx, "appkit metrics server shutdown failed", "error", err.Error())
			}
		}
		if flush != nil {
			return flush(ctx)
		}
		return nil
	}, nil
}

// ---------------------------------------------------------------------------
// app 段：App 实例绑定（无条件触发的阶段 B Provider）
// ---------------------------------------------------------------------------

// appBindingProvider 把 BuildScope.App 绑定给业务侧句柄（appRef）。契约 app
// 段被 bconf.NewBootstrap 预填、恒存在，app 顶层段缺省相位即阶段 B（appkit.
// phaseByTop）——故本 Provider 在每次 Serve 装配中**无条件**触发，App 绑定不
// 依赖任何业务段是否配置。无产物、无清理（纯副作用绑定）。
type appBindingProvider struct{
	// bindApp 由装配方提供（appRef.set）；nil 在 Build 时显式失败（装配缺陷）。
	bindApp func(*appkit.App)
}

func (appBindingProvider) Segments() []string { return []string{"app"} }

func (p appBindingProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, p) }

func (p appBindingProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	if p.bindApp == nil {
		return nil, nil, fmt.Errorf("appBindingProvider: bindApp not wired (assembly bug)")
	}
	p.bindApp(s.App)
	return nil, nil, nil
}
