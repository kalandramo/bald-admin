package app

import (
	"context"
	"os"

	"github.com/spf13/pflag"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	baldbootstrap "github.com/kalandramo/bald/bootstrap"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	otlpcontract "github.com/kalandramo/bald/contrib/observability-otlp/contract"
	s3contract "github.com/kalandramo/bald/oss/s3/contract"
	"github.com/kalandramo/bald/pkg/appkit"
	nacoscontract "github.com/kalandramo/bald/registry/nacos/contract"
	"github.com/kalandramo/bald/transport/asynq"
	asynqcontract "github.com/kalandramo/bald/transport/asynq/contract"
	"github.com/kalandramo/bald/transport/cron"
	croncontract "github.com/kalandramo/bald/transport/cron/contract"
	gatewaycontract "github.com/kalandramo/bald/transport/gateway/contract"
)

// tracerRegistry 显式注册 trace 后端契约 provider（appkit.TracerRegistry：
// 契约 tracer 段 → 全局 TracerProvider，tracer.type 单选）。装配与停机
// flush 由 FromBootstrap 内化。
func tracerRegistry() *appkit.TracerRegistry {
	tr := appkit.NewTracerRegistry()
	tr.MustRegister(otlpcontract.TracerType, otlpcontract.NewTracerProvider("bald-admin"))
	return tr
}

// metricsRegistry 显式注册指标后端契约 provider（appkit.MetricsRegistry：
// 契约 metrics 段 → 双通道装配，metrics.type 单选——"prometheus"=仅本地
// 抓取，"otlp"=抓取+直推，同一实现的两种声明形态）。装配与停机关闭/flush
// 由 FromBootstrap 内化（U1）。

func metricsRegistry() *appkit.MetricsRegistry {
	mr := appkit.NewMetricsRegistry()
	mr.MustRegister(otlpcontract.TypePrometheus, otlpcontract.NewPrometheusProvider("bald-admin"))
	mr.MustRegister(otlpcontract.TypeOTLP, otlpcontract.NewOTLPProvider("bald-admin"))
	return mr
}

// applyObservabilityDefaults 在 FromBootstrap 构造前应用 bald-admin 的
// 可观测性/协议段**缺省合成**（U1：原 setupObservability 的缺省合成半段
// 前移；Build 半段归框架 buildObservability）：
//   - metrics 段缺省 → 合成 `type: prometheus`（仅本地抓取，addr 缺省 :9091，
//     T8 与 gRPC 错峰）——零配置可运行，冒烟/CI 不破；
//   - server.gateway / server.cron 段缺省 → 合成（保持「逃生舱时代总是挂载」
//     的行为；契约装配的语义是「段缺失即不装配」）。
//
// 关于 env（W3 收敛）：本函数**不再**读取任何 env 开关。原
// BALD_ADMIN_METRICS_ADDR / BALD_ADMIN_OTLP_ADDR 的注入已退役——那两个短名
// 会被框架 env 层映射成 `metrics.addr`/`metrics.otlp`（契约无此键，被
// DiscardUnknown 丢弃），且注入发生在契约装载**之前**，会被第二次完整装载
// 覆盖，净效果是「配置声明该段时 env 静默失效」。现统一走框架路径名
// （`BALD_ADMIN_METRICS_PROMETHEUS_ADDR` 等），由装载链自身处理优先级。
//
// 合成值的优先级：显式契约段（配置段/env 路径名）**总是优先**于此处合成——
// 合成仅填空缺（`GetX() == nil` 判据）。
func applyObservabilityDefaults(bootstrap *bootstrapv1.BootstrapConfig, opts *options.ServerOptions) error {
	// metrics 缺省合成：段缺省 → 仅暴露（T9 语义：零配置仍可抓取）。
	// addr 留空即走框架缺省 :9091（见 appkit.StartMetricsServer）。
	if bootstrap.GetMetrics() == nil {
		bootstrap.Metrics = &bootstrapv1.Metrics{Type: otlpcontract.TypePrometheus}
	}

	// Wave 4：server 协议段缺省合成（同 metrics 的「构造前填缺省」范式）。
	//
	// gateway / cron 此前由 bald-admin 的逃生舱**总是挂载**（gateway 由
	// gatewayFactory 注入即挂载；cron 无外部依赖总启用）；迁到契约装配后，
	// 「段缺失 = 不装配」会让它们默认消失——故合成默认段保持既有行为。
	//
	// 合成值来自 ServerOptions（缺省 :8081）。配置段
	// `server.gateway` 或 env `BALD_ADMIN_SERVER_GATEWAY_ADDR` 已提供时，
	// GetGateway() 非 nil，此处跳过。
	if bootstrap.GetServer() != nil {
		if bootstrap.GetServer().GetGateway() == nil && opts.Gateway.Addr != "" {
			bootstrap.GetServer().Gateway = &bootstrapv1.Server_Gateway{Addr: opts.Gateway.Addr}
		}
		if bootstrap.GetServer().GetCron() == nil {
			bootstrap.GetServer().Cron = &bootstrapv1.Server_Cron{}
		}
	}
	return nil
}

// configRegistry 显式注册配置源契约 provider（bootstrap.Registry：契约 config
// 段 → 配置层，注册序即层优先级，先注册者优先）。bald-admin 消费 nacos 配置
// 中心与 kubernetes ConfigMap 两种远程源；file/env 源已由 appkit 自身装配链
// （--config flag / BALD_ADMIN_* env）覆盖，不重复注册。未 import 的后端
// （etcd/consul/apollo/vault/http）零依赖——其契约段无消费者，静默跳过。

func configRegistry() *baldbootstrap.Registry {
	reg := baldbootstrap.NewRegistry()
	// Wave 1a：注册 file / env 两个**离线可用**的配置源。
	//
	// 此前只注册 nacos / kubernetes（两者都需网络）——导致本地开发与 CI
	// 无法用「config 段 + 本地文件」驱动配置（Build 要求至少一个源可构造，
	// 否则报 no config source configured）。补上 file/env 后，无网络环境
	// 也能以 config.file 段启动（端到端验证与离线开发的前提）。
	// 注册序即层优先级：file 在前（本地文件优先于环境变量整文档层）。
	reg.MustRegister("file", baldbootstrap.FileProvider())
	reg.MustRegister("env", baldbootstrap.EnvProvider())
	reg.MustRegister("nacos", baldbootstrap.NacosProvider())
	reg.MustRegister("kubernetes", baldbootstrap.KubernetesProvider())
	return reg
}

// configFileDefault 与 appkit.ConfigFile 的缺省一致（两处必须同步改）。

const configFileDefault = "configs/bald-admin.yaml"

// preloadBootstrap 预装载本地配置文件（--config flag 优先，解析行为与 appkit
// loadConfig 同源：pflag + 未知 flag 白名单）填契约 config 段，并返回配置源
// 注册表（U1：层的 Build 与释放改由 FromBootstrap 经 WithConfigRegistry 内化，
// 原返回 layers+cleanup 的半段删）。两阶段装载的第一阶段——引导信息（远程源
// 地址/凭据/dataId）必须本地可得；层内容（dataId 下的配置文档）在 Run 期
// loadConfig 参与合并。
//
// 预装载是无层的基线装载（本地文件 + env，不含 flag/远程），仅用于读取 config
// 段引导信息；完整契约装载仍由框架 BeforeStart 的 Unmarshal 负责（appkit store
// 合并结果覆盖同一契约指针），两阶段无冲突。

func preloadBootstrap(dst *bootstrapv1.BootstrapConfig) (*baldbootstrap.Registry, error) {
	// 与 appkit 同款解析 --config（appkit 在 Run 期 loadConfig 内解析，预装载
	// 发生在其前，须自行扫描 os.Args）。
	cfgFile := configFileDefault
	fs := pflag.NewFlagSet("pre-config", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.StringVar(&cfgFile, "config", configFileDefault, "config file")
	_ = fs.Parse(os.Args[1:])

	s, err := baldconfig.Load(baldconfig.Options{Name: "bald-admin", ConfigFile: cfgFile})
	if err != nil {
		return nil, err
	}
	defer s.Close() // 预装载 store 无 watch，Close 释放即可
	if err := s.Unmarshal(dst); err != nil {
		return nil, err
	}
	return configRegistry(), nil
}

// registrarRegistry 显式注册注册中心契约 provider（业务按需 import 各后端
// contract 包，未 import 的后端零依赖；registry.type 指向未注册后端时 Build
// fail-fast，而不是静默无注册）。装配表自 2026-09-17 起迁入 bootstrap
// （bootstrap.RegistrarRegistry，与 database/cache/storage 六域对齐）；
// appkit 只留 WithRegistrarRegistry Option 与注册/反注册生命周期编排。

func registrarRegistry() *baldbootstrap.RegistrarRegistry {
	rr := baldbootstrap.NewRegistrarRegistry()
	rr.MustRegister(nacoscontract.Type, nacoscontract.Provider)
	return rr
}

// U1：数据/缓存/存储透传 provider 的注册表（段名即注册 key——database.sql /
// cache.redis / storage.minio）。与 contrib contract 官方 provider 的差异：
// 本业务桥接直接消费 *gorm.DB / *rediscache.Cache / *miniooss.Storage
// （stores/审计/文件模块的既有类型），构造语义（env 优先级、Redis/MinIO 降级）
// 保留在 internal/bootstrap——见 providers.go。

func databaseRegistry() *baldbootstrap.DatabaseRegistry {
	dr := baldbootstrap.NewDatabaseRegistry()
	dr.MustRegister("sql", bootstrappkg.DatabaseProvider)
	return dr
}

func cacheRegistry() *baldbootstrap.CacheRegistry {
	cr := baldbootstrap.NewCacheRegistry()
	cr.MustRegister("redis", bootstrappkg.CacheProvider)
	return cr
}

// serverRegistry 构造 server 协议域的契约装配注册表（Wave 4）。
//
// 注册三个后端子模块提供的 provider（框架不内置，见 bald bootstrap/server.go
// 的 serverSections——asynq/cron/gateway 均为 implemented=false）：
//
//   - asynq：server.asynq 段 → transport/asynq/contract。任务 handler 注册与
//     SetScheduler 接线在 provider 回调内完成（需 bizSet，故经 getter 传入）；
//     段未配 redis_address / codec 时经 resolver 回退（保「配 cache.redis 即启用
//     asynq」与 env BALD_ADMIN_ASYNQ_CODEC 的既有兼容）。
//   - cron：server.cron 段 → transport/cron/contract。周期任务注册在回调内完成。
//   - gateway：server.gateway 段 → transport/gateway/contract。转码注册回调复用
//     本包的 registerGateway（与 HTTP 面同源）。
//
// bizGetter 返回**函数作用域**的 bizSet：provider 回调在 Run 期 server 构造点
// 执行（业务 beforeStart 之后），届时 bizSet 已由 InitializeBiz 赋值——故用
// getter 而非传值，保证读到最新引用。
//
// codecGetter 返回 ServerOptions.Asynq.Codec（env BALD_ADMIN_ASYNQ_CODEC 通道）。
func serverRegistry(
	bizGetter func() *apiserver.BizSet,
	codecGetter func() string,
) *baldbootstrap.ServerRegistry {
	sr := baldbootstrap.NewServerRegistry()

	sr.MustRegister(asynqcontract.Type, asynqcontract.Provider(
		asynqcontract.WithCodecRegistration(registerAsynqCodecs),
		asynqcontract.WithAddressResolver(bootstrappkg.ResolveRedisAddr),
		asynqcontract.WithCodecResolver(codecGetter),
		asynqcontract.WithHandlers(func(ctx context.Context, s *asynq.Server) error {
			if err := registerAsynqHandlers(ctx, s); err != nil {
				return err
			}
			// biz 接线：把 asynq server 适配为 task.Scheduler 注入 biz（Wave 2.3）。
			if bs := bizGetter(); bs != nil && bs.Task != nil {
				bs.Task.SetScheduler(newAsynqScheduler(s))
			}
			return nil
		}),
	))

	sr.MustRegister(croncontract.Type, croncontract.Provider(
		croncontract.WithJobs(func(s *cron.Server) error {
			return registerCronJobs(context.Background(), s)
		}),
	))

	sr.MustRegister(gatewaycontract.Type, gatewaycontract.Provider(registerGateway))

	return sr
}

// buildLoginLimiter 从业务自持配置段构造登录限流器（Wave 1a）。
//
// 配置键（与 file.bucket 同模式，框架契约无对应段）：
//
//	login:
//	  rate_limit:
//	    rate: 1      # 每秒补充令牌数（必填，<=0 视为未配置）
//	    burst: 5     # 突发容量（必填，<=0 视为未配置）
//
// 未配置（rate/burst 任一缺失或非正）→ 返回 nil，biz 保持禁用态（fail-open，
// 不阻断登录）。构造失败（参数非法）同样返回 nil + WARN——限流是防御性增强，
// 不应因配置笔误导致服务无法启动。

func storageRegistry() *baldbootstrap.StorageRegistry {
	sr := baldbootstrap.NewStorageRegistry()
	sr.MustRegister("minio", bootstrappkg.StorageProvider)
	// Wave 5.4：注册 s3 后端（契约 storage.s3 段 → bald/oss/s3）。
	// 官方 provider 签名与 bootstrappkg.StorageProvider 结构化兼容，直接注册。
	sr.MustRegister(s3contract.Type, s3contract.Provider)
	return sr
}
