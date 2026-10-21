package app

// providers.go —— **段驱动 Provider**集合（各段由 Provider 声明，
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

	baldbootstrap "github.com/kalandramo/bald/bootstrap"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	otlpcontract "github.com/kalandramo/bald/contrib/observability-otlp/contract"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/log/bslog"
	"github.com/kalandramo/bald/pkg/appkit"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// configNamespace 是本进程的配置命名空间常量——决定 `--config` 缺省路径
// `configs/{configNamespace}.yaml`（经 appkit.ConfigFile 声明，Serve 内部的
// parseConfigFlag 以它为默认值）。
//
// B6 起 env 前缀（`BALD_ADMIN_*`）与多环境文件名由**契约 app.name 派生**
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
// config 段：配置源层（构造期产层）
// ---------------------------------------------------------------------------

// configSourceProvider 消费契约 config 段（构造期产层）。远程 nacos 源经
// bootstrap.NacosProvider 建层（层语义：Watch=true 参与热更新）；本地文件层
// 由 appkit.ConfigFile 内建。层优先级：本地文件 > 契约源层（nacos）
// ——「远程只补本地未定义的键」。
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
//     官方 Provider 形态不满足（取舍：保留业务 Provider，
//     接缝与 audit 段同款「用户声明自己的 Provider」）；
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
// 既有构造函数（appkit.TracerProvider 函数契约缝）。
// type 分派（当前唯一后端 otlp）收敛到本
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
// B7：暴露端监听面归能力包——contract Provider 经 observability/metricshttp
// 自持起停，返回可关闭的暴露端（appkit.MetricsExposure）；本 Provider 只做
// type 分派转发。停机序由引擎合成：先停暴露端、再 flush 直推通道（收集尾批
// 指标），经 "appkit:metrics-server" Effect 逆序回放最后执行。
type metricsProvider struct{}

func (metricsProvider) Segments() []string { return []string{"metrics"} }

func (metricsProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, metricsProvider{}) }

func (metricsProvider) Build(ctx context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	mc := s.Config.GetMetrics()
	if mc == nil {
		return nil, nil, nil // 段缺省：不装配
	}
	var (
		exp   appkit.MetricsExposure
		flush func(context.Context) error
	)
	switch mc.GetType() {
	case otlpcontract.TypePrometheus:
		e, f, err := otlpcontract.NewPrometheusProvider(configNamespace)(ctx, mc)
		if err != nil {
			return nil, nil, fmt.Errorf("metrics: build prometheus backend: %w", err)
		}
		exp, flush = e, f
	case otlpcontract.TypeOTLP:
		e, f, err := otlpcontract.NewOTLPProvider(configNamespace)(ctx, mc)
		if err != nil {
			return nil, nil, fmt.Errorf("metrics: build otlp backend: %w", err)
		}
		exp, flush = e, f
	default:
		return nil, nil, fmt.Errorf("metrics: unknown metrics.type %q (registered: %q, %q)",
			mc.GetType(), otlpcontract.TypePrometheus, otlpcontract.TypeOTLP)
	}
	return exp, func(ctx context.Context) error {
		if flush != nil {
			return flush(ctx)
		}
		return nil
	}, nil
}

// ---------------------------------------------------------------------------
// logger 段：装饰自持的日志 Provider（B7：WithLogDecorators 取消导出）
// ---------------------------------------------------------------------------

// loggerProvider 消费 logger 段（覆盖 std 默认的 StandardLoggerProvider，段
// 路径末者胜）。B7 后装饰归 provider 自持：本 Provider 沿用内置 slog 分派
// 语义（阶段 A 引导 bslog；阶段 B/热更按 backends 逐项直构 + 多项合并 +
// filter_keys 外包），并把 Attr 层脱敏（bslog.WithFilter——契约 filter_keys
// 漏掉的 slog.Attr 形态由此覆盖，二者互补缺一不可）与固定结构化属性带进
// 阶段 A/B 两个构造点。
type loggerProvider struct{ deco []bslog.Option }

func (p loggerProvider) Segments() []string { return []string{"logger"} }

func (p loggerProvider) Apply(d *appkit.Decl) { d.Providers = append(d.Providers, p) }

func (p loggerProvider) Build(_ context.Context, s appkit.BuildScope) (any, appkit.Cleanup, error) {
	l := s.Config.GetLogger()
	if l == nil {
		// 阶段 A（契约装载前）：引导 bslog——启动期日志仍带脱敏。
		return bslog.New(baldbootstrap.LogOptions(nil), p.deco...), nil, nil
	}
	bs := l.GetBackends()
	if len(bs) == 0 {
		return nil, nil, fmt.Errorf("logger: logger.backends is empty (declare at least one backend, e.g. type \"slog\")")
	}
	loggers := make([]log.Logger, 0, len(bs))
	var cleanups []func()
	for i, b := range bs {
		if b.GetType() != "slog" {
			return nil, nil, fmt.Errorf("logger: logger.backends[%d] type %q: builtin path supports only \"slog\"; remote/custom backends need a logger provider", i, b.GetType())
		}
		lg, c := bslog.NewWithCleanup(baldbootstrap.LogOptions(b), p.deco...)
		loggers = append(loggers, lg)
		cleanups = append(cleanups, c)
	}
	var out log.Logger = loggers[0]
	if len(loggers) > 1 {
		out = log.NewMultiLogger(loggers...)
	}
	merged := func() {
		for _, c := range cleanups {
			c()
		}
	}
	return log.NewFilterLogger(out, l.GetFilterKeys()...), appkit.Cleanup(func(context.Context) error { merged(); return nil }), nil
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
