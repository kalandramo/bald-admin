package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	filev1 "github.com/kalandramo/bald-admin/api/gen/go/file/v1"
	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	permissionv1 "github.com/kalandramo/bald-admin/api/gen/go/permission/v1"
	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/secret/v1"
	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
	"github.com/kalandramo/bald-admin/internal/security/token"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	bconf "github.com/kalandramo/bald/bconf"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	baldbootstrap "github.com/kalandramo/bald/bootstrap"
	obmetrics "github.com/kalandramo/bald/contrib/observability-otlp/metrics"
	"github.com/kalandramo/bald/health"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/log/bslog"
	s3contract "github.com/kalandramo/bald/oss/s3/contract"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/middleware/bundle"
	"github.com/kalandramo/bald/transport"
)

func serveRunE(_ *cobra.Command, _ []string) error {
	// 0. 框架级配置：proto 是唯一真相源，直接持有 Bootstrap 指针。
	//
	// 应用元数据（app.name/version/stop_timeout）与 server.http.addr 均**由配置文件
	// 驱动**（见 configs/bald-admin.yaml 的 app 段与 server 段），此处不再重复赋值——
	// 契约内置缺省（app.name="bald-app" / version="v0.0.0" / stop_timeout=30s /
	// http.addr=":8080"）仅在配置缺失时兜底。
	bootstrap := bconf.NewBootstrap()

	// 业务配置聚合对象**最先创建**——构造期的若干步骤（可观测性 env 开关、
	// gateway/asynq/SSE 地址）需要其默认值通道；实际配置值在各钩子内解码覆盖。
	svrOpts := options.NewServerOptions()

	// 配置源预装载（两阶段装载的第一阶段）：契约 config 段（nacos/kubernetes
	// 的地址/凭据/dataId）是引导信息，必须本地可得——先装载本地配置文件填
	// 契约 config 段。配置层的 Build 由 FromBootstrap 构造期执行
	// （WithConfigRegistry），层在 Run 期 loadConfig 参与合并（优先级低于
	// 本地文件/env/flag：远程只补本地未定义的键）并支持热更新（nacos
	// ListenConfig 推送），层 reader 释放挂框架停机 Effect。
	cfgReg, err := preloadBootstrap(bootstrap)
	if err != nil {
		return fmt.Errorf("bootstrap config sources: %w", err)
	}

	// 可观测性缺省态合成：bald-admin 语义「metrics 段缺省仍暴露
	// :9091」——FromBootstrap 的保守缺省是「段缺省不装配」，故构造前合成默认
	// 段。env 开关（BALD_ADMIN_METRICS_ADDR / BALD_ADMIN_OTLP_ADDR）同处应用；
	// 显式配置源（文件/远程）若配 metrics 段将覆盖合成值——env 开关退居
	// 「段未显式配置时的便捷」，显式声明优先（与契约「配置驱动」哲学一致）。
	if err := applyObservabilityDefaults(bootstrap, svrOpts); err != nil {
		return err
	}

	// 2. 约定装配：Bind×3 / 配置装载+校验 / 日志两阶段 / 热更新 / registrar /
	//    可观测性 / 停机 Effect 全部由 FromBootstrap 内化（详见 newApp）。
	//    biz 构造 + 路由注册 + gRPC 服务注册在 newApp 内的 Run 期
	//    装配钩子里完成（依赖 InitBridges 建立的 store / Redis）。
	//    装配编排抽到 buildApp，使端到端装配测试复用**同一条**生产路径。
	app, _, err := buildApp(bootstrap, svrOpts, cfgReg)
	if err != nil {
		return err
	}

	// 3. 运行。
	if err := app.Run(context.Background()); err != nil {
		baldlog.Error(context.Background(), "bald-admin exited", "error", err)
		return err
	}
	return nil
}

// buildApp 执行**生产装配路径**（构造期骨架 + newApp 内的运行期钩子装配），
// 返回未启动的 AppKit（与 appRef）。
//
// 抽出理由：端到端装配测试需要驱动**真实**的装配路径——历史上认证静默失效、
// 吊销检查失效等真 bug 之所以未被拦截，正是因 e2e 自行拼装、绕过了 main。
// 把编排抽成函数后，测试与生产共用同一实现，装配缺陷不再有藏身处。
func buildApp(
	bootstrap *bootstrapv1.BootstrapConfig,
	svrOpts *options.ServerOptions,
	cfgReg *baldbootstrap.Registry,
) (*appkit.AppKit, *appRefT, error) {
	// 1. 构造期只建伺服面骨架（router + 全局中间件）。**业务装配**（biz 构造、
	//    路由注册、gRPC 服务注册）移至 Run 期装配钩子（见 newApp）——
	//    消除「构造期消费运行期资源（store / Redis）」的时序倒置。
	//
	//    健康检查聚合器：HTTP /healthz /readyz 与 gRPC 标准健康服务状态同源，由
	//    appkit.WithHealth 默认装配（探针路由归装配层，协议实现不注册任何路由）。
	//    本范例尚无依赖项要检查，空聚合器=恒就绪（等价于迁移前的 ready 桩）。
	healthChecker := health.New()

	// 管理面：运行期组件观测与热插拔（工厂目录由业务定义——核心只管挂载原语）。
	// demo.heartbeat 演示带 goroutine 的组件生命周期（Start 起心跳、Dispose 收），与
	// StreamAuditor 同构；admin 角色经 /admin/components 端点挂载/卸载。
	appRef := &appRefT{}
	componentFactories := map[string]apiserver.ComponentFactory{
		"demo.heartbeat": func() appkit.Component { return newHeartbeatComponent(appRef.get) },
	}

	// 横切关注点切 bundle 门面。
	//   - gin 全局层：Recovery→RequestID→Tracing→Logging→CORS→Secure→Authn→Audit
	//     （链序由 bundle 固化）。Authn/Authz 不进全局 bundle——范例用「路由级
	//     分组保护」语义（/v1/login 必须公开），分组链见
	//     internal/apiserver/handler/gin/auth.go；bundle 的全局链模式与分组保护
	//     模式是两种合法模式，范例各保其一（gRPC 侧为全 bundle 链）。
	//   - 增强点：切 bundle 后 /v1/login 也进入审计（此前散装手挂仅审计受保护路由）——
	//     登录失败同样应留审计痕迹。
	//   - 审计层经 bundle.Audit 注入动态转发器（securityaudit.Global，
	//     每次 Record 读全局），而非在构造期快照。若改为构造期快照，会拿到
	//     当时还是 nop 的全局实例（契约轨装配与热切均晚于此处构造），
	//     导致请求审计**静默失效**；收敛进 bundle 后由单层同时承担审计与指标。
	//
	// 契约中间件段（server.http.middleware.*）：由 bundle.FromMiddleware 翻译为
	// Option 后与上述业务 Option 合并。此前该段**七个子段全部零消费者**——配置
	// 写了不生效（最坏形态：看起来生效了）。现在：
	//   - recovery/request_id/logging 段缺失 → 保留 bundle 既有默认（链中仍在）；
	//   - cors/tracing/rate_limit/timeout 段缺失 → 不挂（缺省不装配）；
	//   - 段显式声明但非法（如 timeout.default_timeout_ms<=0）→ 构造期 fail-fast。
	// 注：本仓 configs/bald-admin.yaml 的 server 段未声明 middleware，故默认行为
	// 与迁移前完全一致；在配置里加 middleware 段即生效。
	mwOpts, mwClose, err := bundle.FromMiddleware(
		bootstrap.GetServer().GetHttp().GetMiddleware(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("bundle: build http middleware from config: %w", err)
	}

	ginBundle := bundle.New(append([]bundle.Option{
		bundle.Audit(securityaudit.Global()), // 动态转发：装配/热切对已挂中间件即时生效
		bundle.Metrics(obmetrics.Recorder("bald/example")),
		bundle.Normalized(), // 归一化：审计 object/action 与 gRPC 同源
	}, mwOpts...)...)
	router := gin.New()
	router.Use(ginBundle.Gin()...)

	app, err := newApp(bootstrap, router, cfgReg, healthChecker, svrOpts, appRef, componentFactories, mwClose)
	if err != nil {
		return nil, nil, err
	}
	appRef.set(app) // 管理面 handler 经 appRef 请求期取 AppKit（规避装配时序）
	return app, appRef, nil
}

// NewCommand 构造 bald-admin 根命令。
//
// appkit 自行解析 os.Args 中的 --config / --http.addr 等业务 flag（见
// appkit.loadConfig），故 cobra 需放行未知 flag，避免它因不识别 --config 而提前报错退出。
func NewCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "bald-admin",
		Short: "bald-admin 服务（bald 重构范例）",
		RunE:  serveRunE,
	}
	root.FParseErrWhitelist.UnknownFlags = true
	return root
}

// Execute 构建并执行根命令，返回错误交由调用方（main 包）决定退出码。
//
// kubectl 风格插件发现：首参非已知子命令（且非 flag）时转发到 PATH 中的
// bald-admin-<name>；PATH 中无匹配插件则回落到根命令，由其打印错误并用帮助。
func Execute() error {
	root := NewCommand()
	if len(os.Args) > 1 {
		sub := os.Args[1]
		if !strings.HasPrefix(sub, "-") && !isKnownCommand(root, sub) {
			plugin := "bald-admin-" + sub
			path, err := exec.LookPath(plugin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "unknown command %q (and no plugin %q found in PATH)\n", sub, plugin)
				return root.Execute()
			}
			cmd := exec.Command(path, os.Args[2:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		}
	}
	return root.Execute()
}

func isKnownCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// newApp 用 appkit.FromBootstrap 做约定装配（替代手写 New 装配）。
//
// 框架内化（原手写样板，语义与 New 路径等价）：
//   - Bind×3（server.http / server.grpc / --log.* flag 壳）；
//   - BeforeStart：Settings→Unmarshal→Validate→按契约 logger 段重建 Logger
//     （两阶段日志，脱敏装饰经 WithLogDecorators 两阶段统一生效）；
//   - OnConfigChange：热更新副本试装载+校验后整契约原子落盘并重建 Logger
//     （比原手写版多了 Validate 拦截——坏配置降级保留旧契约，不落半成品）；
//   - app 元数据（name/version/stop_timeout）取自契约 app 段；
//   - registrar：契约 registry 段经 RegistrarRegistry Build，cleanup 挂
//     停机 Effect（原 regRegistry.Build + SetRegistrar + regCleanup 样板）；
//   - 可观测性：tracer/metrics 段经双 Registry 装配，trace shutdown 与
//     metrics 暴露端/flush 挂 Effect 逆序回放最后执行（原
//     setupObservability + observabilityWiring + traceComp 样板）；
//   - 配置层 Build 与释放（原 buildConfigLayers 的 Build 半段 + cleanup defer）。
//
// 业务保留（配置表达不了）：路由、gRPC service、拦截器链序、能力声明、
// 业务桥接（WithBeforeStart——装载链之后契约终值可读）、审计期望态协调、
// gateway 第三服务器（独立 :8081，**已迁契约装配** `server.gateway` 段——见下方
// 说明；此前走 WithExtraServers，因契约 `server.http.driver` 的「同端口二选一」
// 表达不了「gin 主面 + 独立转码面并存」）。
func newApp(
	bootstrap *bootstrapv1.BootstrapConfig,
	router *gin.Engine,
	cfgReg *baldbootstrap.Registry,
	healthChecker *health.Health,
	svrOpts *options.ServerOptions,
	appRef *appRefT,
	componentFactories map[string]apiserver.ComponentFactory,
	mwClose func() error,
) (*appkit.AppKit, error) {
	// biz 在 Run 期装配钩子内构造——InitializeBiz 依赖 InitBridges
	// 建立的 store / Redis，构造期构造会固化 nil（时序倒置）。
	// 框架保证业务 beforeStart 钩子先于 server 构造钩子执行（注册序=执行序，
	// 见 bald pkg/appkit/bootstrap.go），故 registerGRPC 被调用时 bizSet 已就绪。
	//
	// err 与 bizSet 都在**函数作用域**声明：闭包内须用 `=` 赋值（用 `:=` 会在
	// 闭包块内新建同名变量，外层 bizSet 仍为 nil——registerGRPC 读到的即 nil）。
	var (
		bizSet *apiserver.BizSet
		err    error
	)

	// gRPC service 注册回调捕获 Run 期装配的 biz（全部 service 需 biz 注入）。
	// secret 的 DeleteSecret 同经 biz 真实删除（gRPC 直连与 gateway 转码共用）。
	registerGRPC := func(s *grpc.Server) {
		adminv1.RegisterSecretServiceServer(s, secretgrpc.NewServer(bizSet.Secret))
		tenantv1.RegisterTenantServiceServer(s, secretgrpc.NewTenantServer(bizSet.Tenant))
		userv1.RegisterUserServiceServer(s, secretgrpc.NewUserServer(bizSet.User))
		menuv1.RegisterMenuServiceServer(s, secretgrpc.NewMenuServer(bizSet.Menu))
		permissionv1.RegisterPermissionServiceServer(s, secretgrpc.NewPermissionServer(bizSet.Permission))
		dictv1.RegisterDictTypeServiceServer(s, secretgrpc.NewDictTypeServer(bizSet.Dict))
		dictv1.RegisterDictEntryServiceServer(s, secretgrpc.NewDictEntryServer(bizSet.Dict))
		filev1.RegisterFileServiceServer(s, secretgrpc.NewFileServer(bizSet.File))
		auditv1.RegisterAuditServiceServer(s, secretgrpc.NewAuditServer(bizSet.AuditLog))
		// 组织架构（org_unit 7 rpc + position 7 rpc）。
		// 授权 object 经别名层落到策略 "org-units"/"positions"（见 bootstrap.grpcObjectAliases）。
		identityv1.RegisterOrgUnitServiceServer(s, secretgrpc.NewOrgServer(bizSet.Org))
		identityv1.RegisterPositionServiceServer(s, secretgrpc.NewPositionServer(bizSet.Org))
	}

	// app 先声明再进闭包：WithBeforeStart 在 Run 期才执行，届时已赋值
	//（FromBootstrap 同款模式）。
	//
	// authenticator/authorizer 提升到函数作用域——它们在 Run 期
	// 装配钩子内构造（依赖 InitBridges 建立的 store/Redis），再供两个**运行期**
	// 消费者读取：
	//   - appkit.WithGRPCOptions 的选项工厂（gRPC 拦截器链，Run 期 server 构造点求值）；
	//   - appkit.WithExtraServerFunc 的 SSE 工厂。
	// 二者都在业务 beforeStart 之后执行，届时读到真实实例——不再需要「请求期
	// 解析」的 lateAuthn/lateAuthz 适配层。
	//
	// asynq：**已移入 WithExtraServerFunc 工厂**——运行期构造使其能读
	// 契约段 cache.redis；biz 接线（SetScheduler）也在工厂内完成，故不再需要
	// 跨作用域变量（原 asynqSrv 已删）。
	var (
		authenticator authn.Authenticator
		authorizer    authz.Authorizer
	)
	var app *appkit.AppKit
	opts := []appkit.BootstrapOption{
		// --- 能力声明（代码提供） ---
		appkit.WithHTTP(router),
		// 拦截器链改**运行期求值**——工厂在 Run 期 server 构造点
		// 调用（业务 beforeStart 之后），可读上方运行期赋值的 authenticator/authorizer。
		appkit.WithGRPC(registerGRPC),
		appkit.WithGRPCOptions(func() []grpc.ServerOption {
			return newGRPCServerOptions(authenticator, authorizer)
		}),
		// 健康检查默认装配：HTTP 双探针 + gRPC health 状态联动（新版归位后的唯一入口）。
		appkit.WithHealth(healthChecker),

		// 日志装饰：脱敏 + 固定结构化属性。
		//
		// 两套脱敏机制**互补，不是重复**（实测验证）：
		//   - 契约 `logger.filter_keys`（见 configs/bald-admin.yaml）包在
		//     MultiLogger 外层，覆盖**位置参数对**（"password", v）与 **ctx 属性流**；
		//   - 下面 bslog.WithFilter 是 slog handler 层装饰器，覆盖 **slog.Attr**
		//     形式（slog.String("password", v)）。契约版不处理 Attr——其 filterArgs
		//     按「偶数下标为 key」匹配，Attr 不是字符串对，会被漏过。
		// 二者缺一即有来源未被掩码，故并存。
		appkit.WithLogDecorators(
			bslog.WithFilter(bslog.FilterKey("password")),
			bslog.WithFilter(bslog.FilterKey("token")),
			bslog.WithAttrs(slog.String("service.name", "bald-admin")),
		),

		// --- 配置驱动参数 ---
		appkit.WithConfigFile(configFileDefault),
		appkit.WithWatchConfig(true),
		// 业务配置 flag 通道（--login.rate_limit.rate=9 / --gateway.addr=… 等）。
		// 缺此注册时业务 flag 进不了装载 FlagSet，「flag > env > 文件」对业务
		// 配置项整条失效（用户显式传参被静默忽略）。
		appkit.WithBind("", svrOpts),
		// 契约 config 段 → 配置层（注册序=层优先级；Build/释放由框架管）。
		appkit.WithConfigRegistry(cfgReg),

		// 注册中心契约装配（显式注册表声明可用后端，未 import 的后端零依赖；
		// registry 段不支持热更新）。
		appkit.WithRegistrarRegistry(registrarRegistry()),

		// 数据/缓存/存储契约段经透传 provider 消费（构造语义保留业务桥接的
		// env 优先级与降级语义，连接生命周期上挂框架停机 Effect；实例经
		// app.Database/Cache/Storage 取回注入桥接变量，见 WithBeforeStart）。
		appkit.WithDatabaseRegistry(databaseRegistry()),
		appkit.WithCacheRegistry(cacheRegistry()),
		appkit.WithStorageRegistry(storageRegistry()),

		// 可观测性契约装配（tracer/metrics 段；段不支持热更新）。
		appkit.WithTracerRegistry(tracerRegistry()),
		appkit.WithMetricsRegistry(metricsRegistry()),

		// server 协议域契约装配——asynq / cron / gateway 从逃生舱
		// （WithExtraServers / WithExtraServerFunc）迁到契约驱动。
		//
		// provider 在本函数内构造（而非抽到 registries.go 的裸注册）：它们的回调
		// 需读取**函数作用域**的 bizSet —— asynq 的 SetScheduler 与 handler 注册
		// 都在 bizSet 就绪后执行（Run 期 server 构造点，业务 beforeStart 之后）。
		// 传 getter（而非值）保证读到 Run 期赋值后的最新引用。
		appkit.WithServerRegistry(serverRegistry(
			func() *apiserver.BizSet { return bizSet },
			func() string { return svrOpts.Asynq.Codec },
		)),

		// 能力声明（启动期 fail-fast）：BeforeStart 的 InitBridges 将建立真实 DB
		// 连接（BALD_ADMIN_DB_DSN，缺省 SQLite 内存），审计落库（StoreAuditor）依赖它。
		appkit.WithProvides("db"),
		appkit.WithRequires("audit.store", "db"),

		// 增量协调（key 级订阅）：仅当 http.addr 实际变化才触发（同值刷新、
		// 其他 key 变更均不波及），与全量 reload 互补——全量做 Unmarshal 重建，
		// key 级做定点观测/定点重载。
		appkit.WithOnKeyChange("server.http.addr", func(old, new string) {
			baldlog.Info(context.Background(), "server.http.addr changed",
				"old", old, "new", new)
		}),

		// 契约中间件段的资源释放（当前仅 rate_limit 的限流器需要 Close）。
		// 挂停机 Effect：注册在框架 Effect 之后，逆序回放时**先于**框架底座执行
		// （业务资源先收，与 WithEffect 的顺序约定一致）。mwClose 由
		// bundle.FromMiddleware 返回，总非 nil（无资源时为 no-op）。
		appkit.WithEffect("http-middleware", func(context.Context) error {
			return mwClose()
		}),

		// 业务桥接（装载链之后执行——契约终值可读、数据/缓存/存储实例已由
		// 阶段 B 构建）：
		appkit.WithBeforeStart(func(ctx context.Context) error {
			// 装载后的业务配置快照解码进 ServerOptions（覆盖默认值）并校验。
			// 时机：必须在本钩子内（装载链之后，快照才非 nil）；校验 fail-fast，
			// 非法配置在启动期报错而非静默取零值。
			if err := svrOpts.DecodeInto(app.Settings()); err != nil {
				return err
			}
			if err := svrOpts.Validate(); err != nil {
				return err
			}
			// 契约装配实例注入桥接变量（DatabaseProvider 等透传 provider 的
			// 产物；nil 实例 = 降级态，Wire* 对 nil 为 no-op，InitBridges 走自建/降级）。
			if v, ok := app.Database("sql"); ok {
				bootstrappkg.WireDatabase(v)
			}
			if v, ok := app.Cache("redis"); ok {
				bootstrappkg.WireCache(v)
			}
			if v, ok := app.Storage("minio"); ok {
				bootstrappkg.WireStorage(v)
			}
			// storage.type=s3 时装配 s3 后端（与 minio 互斥择一）。
			// WireStorage 按实际类型分派，两者共用 ObjectStorageBridge。
			if v, ok := app.Storage(s3contract.Type); ok {
				bootstrappkg.WireStorage(v)
			}
			// 注入真实依赖配置（业务自持 file.bucket；database/cache/storage
			// 段已由透传 provider 消费，此处仅传桥接所需的余下配置）。
			bootstrappkg.Configure(bootstrap, svrOpts.File.Bucket)
			// 审计兜底租户（可选，缺省 t-default）：login 失败/permission/
			// data_access 三类事件天然无租户，落库时兜底到此值以保证在租户
			// 隔离下仍可见（见 internal/security/audit/record_mapper.go）。
			// **必须在 audit store 构造前调用**——映射在构造期求值。
			securityaudit.SetFallbackTenant(svrOpts.Audit.FallbackTenant)
			// 在 bootstrap 包内装配 bald 桥接（注入 Authenticator / Authorizer /
			// store.RegisterTenant / store.RegisterDataScope）。
			// 仓储经返回值显式取出（不再写包级变量）。
			repos, err := bootstrappkg.InitBridges(ctx)
			if err != nil {
				return fmt.Errorf("init bridges: %w", err)
			}
			// 认证/授权依赖在**路由注册之前**构造完毕——装配已在运行期，
			// 故此处可直接传真实实例（不再需要请求期解析的 lazy 适配器族）。
			//
			// **次序至关重要**：RegisterRoutes 需要带吊销检查的认证器，而吊销检查
			// 依赖 TokenStore（Redis）。若先注册路由再构造 TokenStore，
			// token.NewRevocationChecker 会因 store==nil 退化为纯验签——**吊销静默
			// 失效**（安全回归，正是 lazy 当初要修的 bug）。故 TokenStore 必须在此
			// （路由注册前）就绪。
			tokenStore := buildTokenStore()
			if tokenStore != nil {
				bootstrappkg.TokenStore = tokenStore
			}
			// 认证器：有 Redis 时带吊销检查（登出即时生效）；无 Redis 退化为纯验签。
			// 赋给**函数作用域**变量（非 `:=` 新建）——运行期 gRPC 选项工厂
			// 与 SSE 工厂读取同一实例。
			authenticator = token.NewRevocationChecker(bootstrappkg.Authenticator, tokenStore)
			authorizer = bootstrappkg.Authorizer
			// 业务装配（wire 构造 biz + 路由注册 + gRPC 服务注册）在此
			// 运行期钩子内完成——此刻 store / Redis / TokenStore 已由 InitBridges
			// 建立，biz 不再需要「构造期占位 + 请求期解析」。
			//
			// 时序保证：本钩子（业务 beforeStart）**先于**框架 server 构造钩子执行
			// （注册序=执行序）——故 registerGRPC 被调用时 bizSet 已就绪，
			// WithHTTP(router) 的 handler 也已挂好全部路由。
			bizSet, err = InitializeBiz(repos)
			if err != nil {
				return fmt.Errorf("initialize biz (wire): %w", err)
			}
			// gin 路由注册。认证器/授权器为**真实实例**（上方已构造），
			// 中间件直接持有——无请求期解析层。
			apiserver.RegisterRoutes(router, authenticator, authorizer, bizSet)
			// 管理面：运行期组件观测与热插拔（appRef 在 app.Run 前 set）。
			registerAdminRoutes(router, appRef, authenticator, authorizer, componentFactories)
			// asynq → task.Scheduler 的接线已移入 WithExtraServerFunc
			// 的 asynq 工厂——那里构造后立即 SetScheduler。
			// 文件存储运行期接线。
			//
			// 此处已非必需：InitializeBiz 现于 InitBridges **之后**执行
			// （上方），wire 构造 file biz 时 bootstrap.MinioStorage 已就绪，
			// 「构造期值拷贝拿到 nil」的前提消失。保留为**幂等补注**——降级路径
			// （storage 段未配置 → ObjectStorageBridge/MinioStorage 均 nil）下
			// 两个分支都不写入，biz 内判 nil 返回明确错误，语义不变。
			// 注 ObjectStorageBridge（按 storage.type 已包成 minio/s3
			// 适配器）——minio 与 s3 走同一条接线，签名差异由适配层吸收。
			if bootstrappkg.ObjectStorageBridge != nil {
				bizSet.File.SetObjectStorage(bootstrappkg.ObjectStorageBridge, bootstrappkg.FileBucket)
			} else {
				bizSet.File.SetStorage(bootstrappkg.MinioStorage, bootstrappkg.FileBucket)
			}
			// 同款时序：secret/dict 的 Cache-Aside 接入配置驱动的 RedisCache——
			// cache.redis 段（含 password/db）只流向 bootstrap.RedisCache，wire 的
			// env 通道（BALD_ADMIN_REDIS_ADDR）拿不到完整参数、对带密码实例 ping
			// 即失败；不接线则配置驱动运行下缓存静默失效。未配置段时 RedisCache
			// 为 nil，SetCache 不覆盖，保留 wire env 通道（CI 覆盖手段）。
			// SetCache 接收通用 KV 适配器（cache.Cache），biz 内部包装为
			// loadable 读穿透缓存（loader 构造期绑定，从 key 反解业务参数）。
			if bootstrappkg.RedisCache != nil {
				bizSet.Secret.SetCache(bootstrappkg.RedisCache)
				bizSet.Dict.SetCache(bootstrappkg.RedisCache)
			}
			// 登录限流接线（业务自持配置段 login.rate_limit.*——
			// 与 file.bucket 同模式）。框架契约的 server.http.rate_limit 段是
			// **中间件级**限流且当前零实现（无消费者），故业务级登录限流走自持段。
			// 未配置时保持 nil = 禁用态（不阻断登录，fail-open）。
			if lim := buildLoginLimiter(svrOpts); lim != nil {
				bizSet.Auth.SetLoginLimiter(lim)
			}
			// 登录 DB 查询熔断接线（业务自持配置段 login.breaker.*）。
			// 熔断的意义：DB 故障时快速失败（503），避免所有登录请求都卡在超时上。
			// 用 hystrix（阈值式）而非 sres——sres 是 SRE 概率式，即使全成功也会
			// 概率拒绝且 State 永不返回 Closed（与契约语义不符）。
			if cb := buildLoginBreaker(svrOpts); cb != nil {
				bizSet.Auth.SetLoginBreaker(cb)
			}
			// 登录 DB 查询重试接线（业务自持配置段 login.retry.*）。
			// 与熔断组合：retry 处理偶发抖动，熔断处理持续故障。
			if r := buildLoginRetrier(svrOpts); r != nil {
				bizSet.Auth.SetLoginRetrier(r)
			}
			// 平台级身份判据（跨租户视图）：持有 superadmin 角色的用户签发
			// Platform=true 的令牌，bald pkg/store 据此跳过租户隔离。
			//
			// 判据放在 authmodel.IsPlatformUser（而非此处内联）：测试要复用
			// 同一判据断言，且 bootstrap 播种角色时也引用同一常量。
			//
			// **无条件注入**（不像上面几个 setter 有 nil 分支）——判据是纯函数，
			// 无外部依赖、无时序要求，不存在「未就绪」状态。
			bizSet.Auth.SetPlatformResolver(authmodel.IsPlatformUser)
			// 令牌校验器接线（Logout 吊销 / RefreshToken 刷新 /
			// ValidateToken 校验）。
			//
			// TokenStore 与带吊销检查的认证器已在**路由注册前**构造
			//（见上），此处只把同一实例注入 biz（ValidateToken 与中间件同源，故
			// 「登出后 ValidateToken 报 invalid」与「登出后中间件拒绝」语义一致）。
			if tokenStore != nil {
				bizSet.Auth.SetTokenStore(tokenStore)
				bizSet.Auth.SetAuthenticator(authenticator)
				// 验证码存储（复用同一 Redis）。
				bizSet.Auth.SetCaptchaStore(captcha.NewRedisStore(bootstrappkg.RedisClient))
				// MFA 挑战存储（复用同一 Redis）。
				// Redis 故障时 MFA 走 fail-closed（biz 层判 nil 返回错误）——
				// MFA 是安全边界，不能像限流那样 fail-open。
				if bizSet.MFA != nil {
					bizSet.MFA.SetChallenges(mfa.NewRedisChallengeStore(bootstrappkg.RedisClient))
				}
			} else {
				// 无 Redis：ValidateToken 仍可用（退化为纯验签，无吊销检查）；
				// 验证码不可用（生成/校验返回 503，fail-closed 不放行）。
				bizSet.Auth.SetAuthenticator(bootstrappkg.Authenticator)
			}
			if svrOpts.Auth.AccessTTLMinutes > 0 {
				refresh := time.Duration(0)
				if svrOpts.Auth.RefreshTTLHours > 0 {
					refresh = time.Duration(svrOpts.Auth.RefreshTTLHours) * time.Hour
				}
				bizSet.Auth.SetTokenTTL(time.Duration(svrOpts.Auth.AccessTTLMinutes)*time.Minute, refresh)
			}
			return nil
		}),

		// 期望态协调：以 audit.backends 为期望态，与当前生效后端集合 diff，
		// 自动重建 MultiAuditor 收敛（幂等）。首次收敛在 AfterStart 前（runReconcilers
		// 于基线 loadConfig 后、BeforeStart 链之后触发）；后续 OnConfigChange 携带新
		// 快照再次触发，实现运行期热切换而无需重启。
		appkit.WithReconcile("audit.backends", reconcileAudit),

		appkit.WithAfterStart(func(ctx context.Context) error {
			// 地址为契约最终值（装载后写回；:0 动态端口场景见注册中心聚合结果）。
			ctx = baldlog.ContextWithAttrs(ctx,
				slog.String("stage", "started"),
				slog.String("grpc", bootstrap.GetServer().GetGrpc().GetAddr()))
			baldlog.Info(ctx, "bald-admin started",
				"http", bootstrap.GetServer().GetHttp().GetAddr())
			return nil
		}),
		appkit.WithBeforeStop(func(ctx context.Context) error {
			baldlog.Info(ctx, "bald-admin stopping")
			return nil
		}),
	}

	// gateway 转码面 —— 已迁到契约装配（server.gateway 段，见 WithServerRegistry）。
	// 契约 server.http.driver 的「同端口二选一」表达不了「gin 主面 + 独立转码面并存」，
	// 故用 server.gateway 段（独立 addr + backend_grpc_addr 回退 server.grpc.addr）。

	// asynq 任务队列 —— 已迁到契约装配（server.asynq 段 + asynq contract）。
	// 契约 provider 在 Run 期 server 构造点执行，可读 Run 期资源（cache.redis 段、
	// bizSet）；handler 注册与 SetScheduler 接线都在 provider 回调内完成（见
	// serverRegistry）。段未配 redis_address 时经 WithAddressResolver 回退
	// bootstrap.ResolveRedisAddr（保既有「配 cache.redis 即启用」的兼容）。

	// SSE 传输轴（**运行期构造的逃生舱** WithExtraServerFunc——本仓未提供 sse 的
	// contract provider，且其授权钩子需注入业务 authenticator 函数，声明式契约段
	// 表达不了；详见 sse.go 文件头）。
	// 授权钩子需 authenticator 校验 token（源 HandleAuthorize 同款）。
	//
	// SSE 改**运行期构造**（appkit.WithExtraServerFunc）——工厂在
	// Run 期 server 构造点调用（业务 beforeStart 之后），彼时 InitBridges 已建好
	// authenticator。biz 接线（wireMessageSSE）**一并移入工厂**：工厂返回服务器后
	// 立即接线，故可在同一时机拿到 bizSet（而非等下方业务钩子里再补——那里 sseSrv
	// 还是 nil）。未配置 SSE 时工厂返回 (nil, nil)，框架跳过追加（见 appkit
	// extraServerFns 钩子的 srv != nil 判据）。
	//
	// 注：SSE 保持逃生舱——契约 server.sse 段无 provider，且其授权钩子绑定业务
	// authenticator（契约段的声明式字段表达不了）。与 asynq/cron/gateway 不同：
	// 后三者已由本仓 provider 覆盖（见 serverRegistry）。
	opts = append(opts, appkit.WithExtraServerFunc(func(ctx context.Context) (transport.Server, error) {
		srv, err := buildSSEServer(ctx, loadSSEConfig(svrOpts), authenticator)
		if err != nil {
			return nil, fmt.Errorf("build sse server: %w", err)
		}
		if srv == nil || bizSet == nil {
			return srv, nil
		}
		wireMessageSSE(srv, bizSet.Message)
		return srv, nil
	}))

	// cron 定时器 —— 已迁到契约装配（server.cron 段 + cron contract）。
	// 周期任务注册在 provider 的 WithJobs 回调内完成（见 serverRegistry）。

	// 审计后端 provider 注册（store 后端）。**必须在 FromBootstrap 之前**——
	// registry 是 BootstrapOption；但 provider 本身在阶段 B 才被调用，届时
	// res 已含装配完成的 database 客户端（框架保证 buildAudit 排在
	// buildDatabases 之后），故构造期即取真实 DB——不再有惰性包装。
	opts = append(opts, appkit.WithAuditRegistry(auditRegistry()))

	// 配置命名空间：env 前缀（BALD_ADMIN_*）与多环境文件名（bald-admin-prod.yaml）
	// 的来源。必须是代码层常量——FromBootstrap 路径下若取自契约 app.name
	// 会构成自指（配置文件写 app.name 会让全部 BALD_ADMIN_* 覆盖静默失效）。
	// 取值与上面 bootstrap.GetApp().Name 及 registries.go 预装载的硬编码名一致。
	opts = append(opts, appkit.WithConfigNamespace("bald-admin"))

	app, err = appkit.FromBootstrap(bootstrap, opts...)
	if err != nil {
		// 契约与能力声明不一致（如声明了 WithHTTP 但契约删了 server.http 段）
		// 属启动期错误，fail-fast 暴露，不静默降级。
		return nil, fmt.Errorf("from bootstrap: %w", err)
	}
	return app, nil
}
