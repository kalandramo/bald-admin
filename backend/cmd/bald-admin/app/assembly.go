package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"

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
	"github.com/kalandramo/bald/pkg/middleware/bundle"
	"github.com/kalandramo/bald/transport/asynq"
)

func serveRunE(_ *cobra.Command, _ []string) error {
	// 0. 框架级配置：proto 是唯一真相源，直接持有 Bootstrap 指针。
	bootstrap := bconf.NewBootstrap()
	bootstrap.GetServer().GetHttp().Addr = ":8080"

	// 业务身份默认值（U1）：app 元数据改由契约 app 段驱动（FromBootstrap 内化
	// Name/Version/StopTimeout Option）。env 前缀（BALD_ADMIN_*）由 Name
	// 规范化派生，必须在 FromBootstrap 构造前就位——坑见框架文档「已知耦合」。
	bootstrap.GetApp().Name = "bald-admin"
	bootstrap.GetApp().Version = "v0.1.0"
	bootstrap.GetApp().StopTimeout = durationpb.New(15 * time.Second)

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

	// T9 可观测性缺省态合成（U1）：bald-admin 语义「metrics 段缺省仍暴露
	// :9091」——FromBootstrap 的保守缺省是「段缺省不装配」，故构造前合成默认
	// 段。env 开关（BALD_ADMIN_METRICS_ADDR / BALD_ADMIN_OTLP_ADDR）同处应用；
	// 显式配置源（文件/远程）若配 metrics 段将覆盖合成值——env 开关退居
	// 「段未显式配置时的便捷」，显式声明优先（与契约「配置驱动」哲学一致，
	// 行为收敛见设计文档 U1 变更记录）。
	if err := applyObservabilityDefaults(bootstrap); err != nil {
		return err
	}

	// 1. 业务装配（分层见 internal/apiserver）。M6.4 起由 wire 显式拼装业务对象
	//    （cache / auth biz / secret biz），编译期依赖图校验；框架桥接经
	//    WithBeforeStart 注入（装载链之后，契约终值可读）。
	bizSet, err := InitializeBiz()
	if err != nil {
		return fmt.Errorf("initialize biz (wire): %w", err)
	}
	// 健康检查聚合器：HTTP /healthz /readyz 与 gRPC 标准健康服务状态同源，由
	// appkit.WithHealth 默认装配（探针路由归装配层，协议实现不注册任何路由）。
	// 本范例尚无依赖项要检查，空聚合器=恒就绪（等价于迁移前的 ready 桩）。
	healthChecker := health.New()

	// M10.2 管理面：运行期组件观测与热插拔（工厂目录由业务定义——核心只管挂载原语）。
	// demo.heartbeat 演示带 goroutine 的组件生命周期（Start 起心跳、Dispose 收），与
	// StreamAuditor 同构；admin 角色经 /admin/components 端点挂载/卸载。
	appRef := &appRefT{}
	componentFactories := map[string]apiserver.ComponentFactory{
		"demo.heartbeat": func() appkit.Component { return newHeartbeatComponent(appRef.get) },
	}

	// M10.1（P10 验证）：横切关注点切 bundle 门面。
	//   - gin 全局层：Recovery→RequestID→Logging→Audit（bundle 链序固化）。
	//     Authn/Authz 不进全局 bundle——范例用「路由级分组保护」语义（/v1/login 必须公开），
	//     分组链见 internal/apiserver/handler/gin/auth.go；bundle 的全局链模式与
	//     分组保护模式是两种合法模式，范例各保其一（gRPC 侧为全 bundle 链）。
	//   - 增强点：切 bundle 后 /v1/login 也进入审计（此前散装手挂仅审计受保护路由）——
	//     登录失败同样应留审计痕迹。
	//   - T6 修复：审计层经 bundle.Audit 注入动态转发器（securityaudit.Global，
	//     每次 Record 读全局）。此前散装 AuditMiddleware 在 main 期构造即快照
	//     全局 nop（契约轨 BeforeStart 装配、R1-2 热切均晚于构造），请求审计
	//     静默失效；收敛进 bundle 后由单层同时承担审计与指标（M8 同源 emit）。
	ginBundle := bundle.New(
		bundle.Audit(securityaudit.Global()), // 动态转发：装配/热切对已挂中间件即时生效
		bundle.Metrics(obmetrics.Recorder("bald/example")),
		bundle.Normalized(), // P9 归一化：审计 object/action 与 gRPC 同源
	)
	router := gin.New()
	router.Use(ginBundle.Gin()...)
	apiserver.RegisterRoutes(router, bizSet)                // gin handler 路由（T10：BizSet 直传）
	registerAdminRoutes(router, appRef, componentFactories) // M10.2 管理面（appRef 迟到绑定）

	// 2. 约定装配（U1）：Bind×3 / 配置装载+校验 / 日志两阶段 / 热更新 / registrar /
	//    可观测性 / 停机 Effect 全部由 FromBootstrap 内化（详见 newApp）。
	app, err := newApp(bootstrap, router, bizSet, cfgReg, healthChecker)
	if err != nil {
		return err
	}
	appRef.set(app) // M10.2：管理面 handler 经 appRef 请求期取 AppKit（规避装配时序）

	// 3. 运行。
	if err := app.Run(context.Background()); err != nil {
		baldlog.Error(context.Background(), "bald-admin exited", "error", err)
		return err
	}
	return nil
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

// newApp 用 appkit.FromBootstrap 做约定装配（U1：从 New 手动装配切换）。
//
// 框架内化（原手写样板，语义与 New 路径等价）：
//   - Bind×3（server.http / server.grpc / --log.* flag 壳）；
//   - BeforeStart：Settings→Unmarshal→Validate→按契约 logger 段重建 Logger
//     （两阶段日志，脱敏装饰经 WithLogDecorators 两阶段统一生效）；
//   - OnConfigChange：热更新副本试装载+校验后整契约原子落盘并重建 Logger
//     （比原手写版多了 Validate 拦截——坏配置降级保留旧契约，不落半成品）；
//   - app 元数据（name/version/stop_timeout）取自契约 app 段；
//   - T7 registrar：契约 registry 段经 RegistrarRegistry Build，cleanup 挂
//     停机 Effect（原 regRegistry.Build + SetRegistrar + regCleanup 样板）；
//   - T9 可观测性：tracer/metrics 段经双 Registry 装配，trace shutdown 与
//     metrics 暴露端/flush 挂 Effect 逆序回放最后执行（原 setupObservability
//   - observabilityWiring + traceComp 样板）；
//   - 配置层 Build 与释放（原 buildConfigLayers 的 Build 半段 + cleanup defer）。
//
// 业务保留（配置表达不了）：路由、gRPC service、拦截器链序、S1 能力声明、
// 业务桥接（WithBeforeStart——装载链之后契约终值可读）、R1-2 审计协调、
// gateway 第三服务器（独立 :8081，WithExtraServers 逃生舱——契约
// server.http.driver 的网关面模式与「gin 主面 + 独立转码面并存」不匹配）。
func newApp(
	bootstrap *bootstrapv1.BootstrapConfig,
	router http.Handler,
	bizSet *apiserver.BizSet,
	cfgReg *baldbootstrap.Registry,
	healthChecker *health.Health,
) (*appkit.AppKit, error) {
	// T2：gRPC service 注册回调捕获 wire 装配的 biz（全部 service 需 biz 注入）。
	// secret 的 DeleteSecret 同经 biz 真实删除（gRPC 直连与 gateway 转码共用）。
	registerGRPC := func(s *grpc.Server) {
		adminv1.RegisterSecretServiceServer(s, secretgrpc.NewServer(bizSet.Secret))
		tenantv1.RegisterTenantServiceServer(s, secretgrpc.NewTenantServer(bizSet.Tenant))
		userv1.RegisterUserServiceServer(s, secretgrpc.NewUserServer(bizSet.User))
		menuv1.RegisterMenuServiceServer(s, secretgrpc.NewMenuServer(bizSet.Menu))
		permissionv1.RegisterPermissionServiceServer(s, secretgrpc.NewPermissionServer(bizSet.Permission))
		dictv1.RegisterDictTypeServiceServer(s, secretgrpc.NewDictTypeServer(bizSet.Dict))
		dictv1.RegisterDictEntryServiceServer(s, secretgrpc.NewDictEntryServer(bizSet.Dict))
		filev1.RegisterFileServiceServer(s, secretgrpc.NewFileServer(bizSet.File))        // T5
		auditv1.RegisterAuditServiceServer(s, secretgrpc.NewAuditServer(bizSet.AuditLog)) // T6
		// Wave 1.7 补齐：组织架构（org_unit 7 rpc + position 7 rpc）。
		// 授权 object 经别名层落到策略 "org-units"/"positions"（见 bootstrap.grpcObjectAliases）。
		identityv1.RegisterOrgUnitServiceServer(s, secretgrpc.NewOrgServer(bizSet.Org))
		identityv1.RegisterPositionServiceServer(s, secretgrpc.NewPositionServer(bizSet.Org))
	}

	// app 先声明再进闭包：WithBeforeStart 在 Run 期才执行，届时已赋值
	//（FromBootstrap 同款模式）。
	var app *appkit.AppKit
	opts := []appkit.BootstrapOption{
		// --- 能力声明（代码提供） ---
		appkit.WithHTTP(router),
		appkit.WithGRPC(registerGRPC, newGRPCServerOptions()...),
		// 健康检查默认装配：HTTP 双探针 + gRPC health 状态联动（新版归位后的唯一入口）。
		appkit.WithHealth(healthChecker),

		// 日志脱敏装饰：阶段 A（启动默认）/ 阶段 B（契约重建）统一生效
		//（原 setLogger 两处手挂收敛至此）。
		appkit.WithLogDecorators(
			bslog.WithFilter(bslog.FilterKey("password")),
			bslog.WithFilter(bslog.FilterKey("token")),
			bslog.WithAttrs(slog.String("service.name", "bald-admin")),
		),

		// --- 配置驱动参数 ---
		appkit.WithConfigFile(configFileDefault),
		appkit.WithWatchConfig(true),
		// 契约 config 段 → 配置层（注册序=层优先级；Build/释放由框架管）。
		appkit.WithConfigRegistry(cfgReg),

		// T7：注册中心契约装配（显式注册表声明可用后端，未 import 的后端零依赖；
		// registry 段不支持热更新）。
		appkit.WithRegistrarRegistry(registrarRegistry()),

		// U1：数据/缓存/存储契约段经透传 provider 消费（构造语义保留业务桥接的
		// env 优先级与降级语义，连接生命周期上挂框架停机 Effect；实例经
		// app.Database/Cache/Storage 取回注入桥接变量，见 WithBeforeStart）。
		appkit.WithDatabaseRegistry(databaseRegistry()),
		appkit.WithCacheRegistry(cacheRegistry()),
		appkit.WithStorageRegistry(storageRegistry()),

		// T9：可观测性契约装配（tracer/metrics 段；段不支持热更新）。
		appkit.WithTracerRegistry(tracerRegistry()),
		appkit.WithMetricsRegistry(metricsRegistry()),

		// S1 能力声明（启动期 fail-fast）：BeforeStart 的 InitBridges 将建立真实 DB
		// 连接（BALD_ADMIN_DB_DSN，缺省 SQLite 内存），审计落库（StoreAuditor）依赖它。
		appkit.WithProvides("db"),
		appkit.WithRequires("audit.store", "db"),

		// R1 增量协调（key 级订阅）：仅当 http.addr 实际变化才触发（同值刷新、
		// 其他 key 变更均不波及），与全量 reload 互补——全量做 Unmarshal 重建，
		// key 级做定点观测/定点重载。
		appkit.WithOnKeyChange("server.http.addr", func(old, new string) {
			baldlog.Info(context.Background(), "server.http.addr changed",
				"old", old, "new", new)
		}),

		// 业务桥接（装载链之后执行——契约终值可读、数据/缓存/存储实例已由
		// 阶段 B 构建）：
		appkit.WithBeforeStart(func(ctx context.Context) error {
			// U1：契约装配实例注入桥接变量（DatabaseProvider 等透传 provider 的
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
			// Wave 5.4：storage.type=s3 时装配 s3 后端（与 minio 互斥择一）。
			// WireStorage 按实际类型分派，两者共用 ObjectStorageBridge。
			if v, ok := app.Storage(s3contract.Type); ok {
				bootstrappkg.WireStorage(v)
			}
			// T0：注入真实依赖配置（业务自持 file.bucket；database/cache/storage
			// 段已由透传 provider 消费，此处仅传桥接所需的余下配置）。
			bootstrappkg.Configure(bootstrap, app.Config().GetString("file.bucket"))
			// 审计兜底租户（可选，缺省 t-default）：login 失败/permission/
			// data_access 三类事件天然无租户，落库时兜底到此值以保证在租户
			// 隔离下仍可见（见 internal/security/audit/record_mapper.go）。
			// **必须在 audit store 构造前调用**——映射在构造期求值。
			securityaudit.SetFallbackTenant(app.Config().GetString("audit.fallback_tenant"))
			// 在 bootstrap 包内装配 bald 桥接（P7/P8/P9 注册点）：M1+ 注入
			// Authenticator / Authorizer / store.RegisterTenant / store.RegisterDataScope。
			if err := bootstrappkg.InitBridges(ctx); err != nil {
				return fmt.Errorf("init bridges: %w", err)
			}
			// T8：文件存储运行期接线——InitializeBiz 构造期值拷贝 bootstrap.MinioStorage
			// 拿到 nil（InitBridges 尚未执行，§9 真调暴露的 e2e 盲区），桥接装配后补注。
			// Wave 5.4：改注入 ObjectStorageBridge（按 storage.type 已包成 minio/s3
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
			// D1：SetCache 接收通用 KV 适配器（cache.Cache），biz 内部包装为
			// loadable 读穿透缓存（loader 构造期绑定，从 key 反解业务参数）。
			if bootstrappkg.RedisCache != nil {
				bizSet.Secret.SetCache(bootstrappkg.RedisCache)
				bizSet.Dict.SetCache(bootstrappkg.RedisCache)
			}
			// Wave 1a：登录限流接线（业务自持配置段 login.rate_limit.*——
			// 与 file.bucket 同模式）。框架契约的 server.http.rate_limit 段是
			// **中间件级**限流且当前零实现（无消费者），故业务级登录限流走自持段。
			// 未配置时保持 nil = 禁用态（不阻断登录，fail-open）。
			if lim := buildLoginLimiter(app.Config()); lim != nil {
				bizSet.Auth.SetLoginLimiter(lim)
			}
			// Wave 1b：登录 DB 查询熔断接线（业务自持配置段 login.breaker.*）。
			// 熔断的意义：DB 故障时快速失败（503），避免所有登录请求都卡在超时上。
			// 用 hystrix（阈值式）而非 sres——sres 是 SRE 概率式，即使全成功也会
			// 概率拒绝且 State 永不返回 Closed（与契约语义不符，见 Wave 1b 报告）。
			if cb := buildLoginBreaker(app.Config()); cb != nil {
				bizSet.Auth.SetLoginBreaker(cb)
			}
			// Wave 1c：登录 DB 查询重试接线（业务自持配置段 login.retry.*）。
			// 与熔断组合：retry 处理偶发抖动，熔断处理持续故障。
			if r := buildLoginRetrier(app.Config()); r != nil {
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
			// Wave 1d：令牌存储 + 校验器接线（Logout 吊销 / RefreshToken 刷新 /
			// ValidateToken 校验）。
			//
			// 关键时序：RegisterRoutes 在**装配期**（本函数返回前）执行，而
			// RedisClient 在此刻（BeforeStart 内）才就绪——故路由层用的是
			// LazyAuthenticatorWithRevocation（请求期读包级 TokenStore），
			// 此处只需把 TokenStore 赋好。
			//
			// ValidateToken 的校验器同样用带吊销检查的认证器——与中间件同源，
			// 故「登出后 ValidateToken 报 invalid」与「登出后中间件拒绝」语义一致。
			if ts := buildTokenStore(); ts != nil {
				bootstrappkg.TokenStore = ts
				bizSet.Auth.SetTokenStore(ts)
				bizSet.Auth.SetAuthenticator(bootstrappkg.LazyAuthenticatorWithRevocation())
				// Wave 1d-2：验证码存储（复用同一 Redis）。
				bizSet.Auth.SetCaptchaStore(captcha.NewRedisStore(bootstrappkg.RedisClient))
				// Wave 1.5：MFA 挑战存储（复用同一 Redis）。
				// Redis 故障时 MFA 走 fail-closed（biz 层判 nil 返回错误）——
				// MFA 是安全边界，不能像限流那样 fail-open。
				if bizSet.MFA != nil {
					bizSet.MFA.SetChallenges(mfa.NewRedisChallengeStore(bootstrappkg.RedisClient))
				}
			} else {
				// 无 Redis：ValidateToken 仍可用（退化为纯验签，无吊销检查）；
				// 验证码不可用（生成/校验返回 503，fail-closed 不放行）。
				bizSet.Auth.SetAuthenticator(bootstrappkg.LazyAuthenticator())
			}
			if v := configFloat(app.Config(), "auth.access_ttl_minutes"); v > 0 {
				refresh := time.Duration(0)
				if rv := configFloat(app.Config(), "auth.refresh_ttl_hours"); rv > 0 {
					refresh = time.Duration(rv) * time.Hour
				}
				bizSet.Auth.SetTokenTTL(time.Duration(v)*time.Minute, refresh)
			}
			return nil
		}),

		// R1-2 期望态协调：以 audit.backends 为期望态，与当前生效后端集合 diff，
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

	// M5：gateway 第三服务器（REST → gRPC 转码，独立 :8081 与 HTTP 主服务错峰）。
	// WithExtraServers 逃生舱：契约 server.http.driver 的网关面模式是「同一端口
	// 二选一」，与本范例「gin 主面 + 独立转码面并存」不匹配。
	opts = append(opts, appkit.WithExtraServers(buildGateway(bootstrap)...))

	// Wave 2.1：asynq 任务队列服务器（逃生舱，决策见 asynq.go 文件头）。
	// 无 Redis 地址时 buildAsynqServer 返回 nil，WithExtraServers 收到 nil 会
	// panic——故先判空再追加。
	asynqSrv, aerr := buildAsynqServer(context.Background(), asynqRedisAddr())
	if aerr != nil {
		return nil, fmt.Errorf("build asynq server: %w", aerr)
	}
	// Wave 3.1/3.2：SSE 传输轴（逃生舱——框架不装配 server.sse，见 sse.go 文件头）。
	// 授权钩子需 authenticator 校验 token（源 HandleAuthorize 同款）。
	if sseSrv, serr := buildSSEServer(context.Background(), loadSSEConfig(),
		bootstrappkg.LazyAuthenticator()); serr != nil {
		return nil, fmt.Errorf("build sse server: %w", serr)
	} else if sseSrv != nil {
		opts = append(opts, appkit.WithExtraServers(sseSrv))
		// Wave 3.2：把 message 域接到 SSE（源 RegisterInternalMessagePublisher）。
		// SSE 未装配时 wireMessageSSE 返回原 biz——降级为「只落库不推送」。
		wireMessageSSE(sseSrv, bizSet.Message)
	}

	// Wave 2.4：cron 定时器（逃生舱，决策与约束见 cron.go 文件头）。
	// cron 无需外部依赖，总是启用。
	if cronSrv, cerr := buildCronServer(context.Background()); cerr != nil {
		return nil, fmt.Errorf("build cron server: %w", cerr)
	} else if cronSrv != nil {
		opts = append(opts, appkit.WithExtraServers(cronSrv))
	}

	if asynqSrv != nil {
		opts = append(opts, appkit.WithExtraServers(asynqSrv))
		// Wave 2.3：把 asynq server 适配为 task.Scheduler 注入 biz。
		// 类型断言取回 *asynq.Server（buildAsynqServer 的返回类型是接口）。
		if as, ok := asynqSrv.(*asynq.Server); ok && bizSet != nil && bizSet.Task != nil {
			bizSet.Task.SetScheduler(newAsynqScheduler(as))
		}
	}

	// D14 修复：注册审计后端 provider（store 惰性绑定 DB）。
	// **必须在 FromBootstrap 之前**——registry 是 BootstrapOption；
	// 但传入的是惰性工厂，真正的 DB 解析推迟到运行期首个审计事件
	// （见 audit_wiring.go 文件头：appkit 的 buildAudit 先于 buildDatabases）。
	opts = append(opts, appkit.WithAuditRegistry(auditRegistry()))

	app, err := appkit.FromBootstrap(bootstrap, opts...)
	if err != nil {
		// 契约与能力声明不一致（如声明了 WithHTTP 但契约删了 server.http 段）
		// 属启动期错误，fail-fast 暴露，不静默降级。
		return nil, fmt.Errorf("from bootstrap: %w", err)
	}
	return app, nil
}

// tracerRegistry 显式注册 trace 后端契约 provider（appkit.TracerRegistry：
// 契约 tracer 段 → 全局 TracerProvider，tracer.type 单选）。装配与停机
// flush 由 FromBootstrap 内化（U1：原 setupObservability + traceComp 样板删）。
