// Command go-bald-admin 是 go-bald-admin 服务入口：
// 用 bald appkit 组合一个 HTTP 服务与一个 gRPC 服务（双协议），
// 作为用 bald 重构 go-wind-admin/backend 的官方范例（验证 P0–P9）。
//
// 运行：
//
//	go run ./examples/go-bald-admin --config=examples/go-bald-admin/configs/go-bald-admin.yaml
//	go run ./examples/go-bald-admin --http.addr=:18080
//	GO_BALD_ADMIN_SERVER_HTTP_ADDR=:18080 go run ./examples/go-bald-admin
//	（env 前缀由 app name 规范化派生：go-bald-admin → GO_BALD_ADMIN_，下划线即点路径分隔）
//
//	# 验证 HTTP 路由
//	curl -i http://127.0.0.1:8080/v1/ping
//	curl -i http://127.0.0.1:8080/v1/info
//
// 设计见 examples/go-bald-admin/docs/设计文档.md；分层参考 osbuilder 脚手架模板。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	filev1 "github.com/kalandramo/bald-admin/api/gen/go/file/v1"
	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	permissionv1 "github.com/kalandramo/bald-admin/api/gen/go/permission/v1"
	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/secret/v1"
	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
	"github.com/kalandramo/bald-admin/internal/security/token"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	bconf "github.com/kalandramo/bald/bconf"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	baldbootstrap "github.com/kalandramo/bald/bootstrap"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	otlpcontract "github.com/kalandramo/bald/contrib/observability-otlp/contract"
	obmetrics "github.com/kalandramo/bald/contrib/observability-otlp/metrics"
	"github.com/kalandramo/bald/health"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/log/bslog"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/circuitbreaker"
	"github.com/kalandramo/bald/circuitbreaker/hystrix"
	"github.com/kalandramo/bald/ratelimit"
	"github.com/kalandramo/bald/retry"
	"github.com/kalandramo/bald/transport/asynq"
	"github.com/kalandramo/bald/ratelimit/tokenbucket"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/middleware/bundle"
	nacoscontract "github.com/kalandramo/bald/registry/nacos/contract"
	s3contract "github.com/kalandramo/bald/oss/s3/contract"
	"github.com/kalandramo/bald/transport"
	gateway "github.com/kalandramo/bald/transport/gateway"
)

func serveRunE(_ *cobra.Command, _ []string) error {
	// 0. 框架级配置：proto 是唯一真相源，直接持有 Bootstrap 指针。
	bootstrap := bconf.NewBootstrap()
	bootstrap.GetServer().GetHttp().Addr = ":8080"

	// 业务身份默认值（U1）：app 元数据改由契约 app 段驱动（FromBootstrap 内化
	// Name/Version/StopTimeout Option）。env 前缀（GO_BALD_ADMIN_*）由 Name
	// 规范化派生，必须在 FromBootstrap 构造前就位——坑见框架文档「已知耦合」。
	bootstrap.GetApp().Name = "go-bald-admin"
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

	// T9 可观测性缺省态合成（U1）：go-bald-admin 语义「metrics 段缺省仍暴露
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
		baldlog.Error(context.Background(), "go-bald-admin exited", "error", err)
		return err
	}
	return nil
}

func main() {
	root := &cobra.Command{
		Use:   "go-bald-admin",
		Short: "go-bald-admin 服务（bald 重构范例）",
		RunE:  serveRunE,
	}
	// appkit 自行解析 os.Args 中的 --config / --http.addr 等业务 flag（见 appkit.loadConfig），
	// 故 cobra 需放行未知 flag，避免它因不识别 --config 而提前报错退出。
	root.FParseErrWhitelist.UnknownFlags = true

	// kubectl 风格插件发现：首参非已知子命令（且非 flag）时转发到 PATH 中的 go-bald-admin-<name>。
	if len(os.Args) > 1 {
		sub := os.Args[1]
		if !strings.HasPrefix(sub, "-") && !isKnownCommand(root, sub) {
			plugin := "go-bald-admin-" + sub
			path, err := exec.LookPath(plugin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "unknown command %q (and no plugin %q found in PATH)\n", sub, plugin)
				if err := root.Execute(); err != nil {
					osExit(1)
				}
				return
			}
			cmd := exec.Command(path, os.Args[2:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				osExit(1)
			}
			return
		}
	}

	if err := root.Execute(); err != nil {
		osExit(1)
	}
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
			bslog.WithAttrs(slog.String("service.name", "go-bald-admin")),
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
			baldlog.Info(ctx, "go-bald-admin started",
				"http", bootstrap.GetServer().GetHttp().GetAddr())
			return nil
		}),
		appkit.WithBeforeStop(func(ctx context.Context) error {
			baldlog.Info(ctx, "go-bald-admin stopping")
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
func tracerRegistry() *appkit.TracerRegistry {
	tr := appkit.NewTracerRegistry()
	tr.MustRegister(otlpcontract.TracerType, otlpcontract.NewTracerProvider("go-bald-admin"))
	return tr
}

// metricsRegistry 显式注册指标后端契约 provider（appkit.MetricsRegistry：
// 契约 metrics 段 → 双通道装配，metrics.type 单选——"prometheus"=仅本地
// 抓取，"otlp"=抓取+直推，同一实现的两种声明形态）。装配与停机关闭/flush
// 由 FromBootstrap 内化（U1）。
func metricsRegistry() *appkit.MetricsRegistry {
	mr := appkit.NewMetricsRegistry()
	mr.MustRegister(otlpcontract.TypePrometheus, otlpcontract.NewPrometheusProvider("go-bald-admin"))
	mr.MustRegister(otlpcontract.TypeOTLP, otlpcontract.NewOTLPProvider("go-bald-admin"))
	return mr
}

// applyObservabilityDefaults 在 FromBootstrap 构造前应用 go-bald-admin 的
// 可观测性缺省态与 env 开关（U1：原 setupObservability 的缺省合成/env 半段
// 前移——Build 半段归框架 buildObservability）：
//   - metrics 段缺省 → 合成 `type: prometheus`（仅本地抓取，addr 缺省 :9091，
//     T8 与 gRPC 错峰）——零配置可运行，冒烟/CI 不破；
//   - BALD_ADMIN_METRICS_ADDR 覆盖暴露端口；BALD_ADMIN_OTLP_ADDR 覆盖双通道
//     endpoint——只提供地址，不改变 type 声明的语义（配 prometheus 不会因
//     env 翻转成推送；otlp endpoint 与 type=prometheus 的矛盾声明仍报错）。
//
// 时机语义（U1 行为收敛）：env 开关在构造前应用于合成/预装载态；显式配置源
// （文件/远程）若配了 metrics/tracer 段，装载时覆盖 env 值——显式声明优先
// 于 env 开关（原 New 路径 env 后置于装载，语义为 env > 文件；收敛原因：
// FromBootstrap 的 Build 在装载后立即执行，业务无介入位，且「配置说开了」
// 胜过「环境变量说没开」与契约哲学一致）。
func applyObservabilityDefaults(bootstrap *bootstrapv1.BootstrapConfig) error {
	// metrics 缺省合成：段缺省 → 仅暴露（T9 语义：零配置仍可抓取）。
	if bootstrap.GetMetrics() == nil {
		bootstrap.Metrics = &bootstrapv1.Metrics{Type: otlpcontract.TypePrometheus}
	}
	m := bootstrap.GetMetrics()
	if v := os.Getenv("BALD_ADMIN_METRICS_ADDR"); v != "" {
		if m.Prometheus == nil {
			m.Prometheus = &bootstrapv1.Metrics_Prometheus{}
		}
		m.Prometheus.Addr = v
	}
	if v := os.Getenv("BALD_ADMIN_OTLP_ADDR"); v != "" {
		switch m.GetType() {
		case otlpcontract.TypeOTLP:
			if m.Otlp == nil {
				m.Otlp = &bootstrapv1.Metrics_Otlp{}
			}
			m.Otlp.Endpoint = v
		case otlpcontract.TypePrometheus:
			return fmt.Errorf("metrics.type=prometheus conflicts with BALD_ADMIN_OTLP_ADDR configured (use type \"otlp\" to enable push)")
		}
		if tr := bootstrap.GetTracer(); tr != nil {
			if tr.Otlp == nil {
				tr.Otlp = &bootstrapv1.Tracer_Otlp{}
			}
			tr.Otlp.Endpoint = v
		}
	}
	return nil
}

// configRegistry 显式注册配置源契约 provider（bootstrap.Registry：契约 config
// 段 → 配置层，注册序即层优先级，先注册者优先）。go-bald-admin 消费 nacos 配置
// 中心与 kubernetes ConfigMap 两种远程源；file/env 源已由 appkit 自身装配链
// （--config flag / GO_BALD_ADMIN_* env）覆盖，不重复注册。未 import 的后端
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
const configFileDefault = "configs/go-bald-admin.yaml"

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

	s, err := baldconfig.Load(baldconfig.Options{Name: "go-bald-admin", ConfigFile: cfgFile})
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
func buildLoginLimiter(cfg *baldconfig.Store) ratelimit.Limiter {
	rate := configFloat(cfg, "login.rate_limit.rate")
	burst := configFloat(cfg, "login.rate_limit.burst")
	if rate <= 0 || burst <= 0 {
		return nil // 未配置或配置不全：禁用态
	}
	lim, err := tokenbucket.New(rate, burst)
	if err != nil {
		baldlog.Warn(context.Background(), "login rate limiter disabled: invalid config",
			"rate", rate, "burst", burst, "error", err.Error())
		return nil
	}
	baldlog.Info(context.Background(), "login rate limiter enabled", "rate", rate, "burst", burst)
	return lim
}

// buildLoginBreaker 从业务自持配置段构造登录 DB 查询熔断器（Wave 1b）。
//
// 配置键：
//
//	login:
//	  breaker:
//	    error_threshold: 0.5        # 错误率阈值（0-1），default 0.5
//	    request_volume: 20          # 最小请求量（低于此不评估），default 20
//	    sleep_window_seconds: 5     # Open 后多久转半开（秒），default 5
//
// 全部字段缺省时返回 nil（禁用态）——熔断是防御性增强，未配置则不介入。
// 用 sres 之外的 **hystrix**（阈值式）：sres 的概率式语义不符合契约对
// StateClosed 的定义（详见 Wave 1b 交付报告与 e2e 测试注释）。
func buildLoginBreaker(cfg *baldconfig.Store) circuitbreaker.CircuitBreaker {
	_, configured := cfg.Get("login.breaker")
	if !configured {
		return nil // 未配置：禁用态
	}
	opts := []hystrix.Option{}
	if v := configFloat(cfg, "login.breaker.error_threshold"); v > 0 {
		opts = append(opts, hystrix.WithErrorThreshold(v))
	}
	if v := configFloat(cfg, "login.breaker.request_volume"); v > 0 {
		opts = append(opts, hystrix.WithRequestVolumeThreshold(int(v)))
	}
	if v := configFloat(cfg, "login.breaker.sleep_window_seconds"); v > 0 {
		opts = append(opts, hystrix.WithSleepWindow(time.Duration(v)*time.Second))
	}
	cb := hystrix.New(opts...)
	baldlog.Info(context.Background(), "login breaker enabled")
	return cb
}

// buildLoginRetrier 从业务自持配置段构造登录 DB 查询重试器（Wave 1c）。
//
// 配置键：
//
//	login:
//	  retry:
//	    max_attempts: 3             # 最大尝试次数（含首次），default 3
//	    initial_backoff_ms: 100     # 首次退避（毫秒），default 200
//	    max_backoff_ms: 2000        # 退避上限（毫秒），default 10000
//
// 整段缺省时返回 nil（禁用态，单次查询）。
// 分类器固定为 authbiz.RetryOnTransientDBError——**只重试瞬时故障**，
// 不重试 ErrNotFound（确定性业务结果，重试只是浪费 DB 往返）。
func buildLoginRetrier(cfg *baldconfig.Store) *retry.Retrier {
	if _, configured := cfg.Get("login.retry"); !configured {
		return nil // 未配置：禁用态
	}
	opts := []retry.Option{
		retry.WithClassifier(authbiz.RetryOnTransientDBError),
	}
	if v := configFloat(cfg, "login.retry.max_attempts"); v >= 1 {
		opts = append(opts, retry.WithMaxAttempts(int(v)))
	}
	backoff := retry.ExponentialBackoff{
		Initial: 200 * time.Millisecond,
		Factor:  2,
		Max:     10 * time.Second,
	}
	if v := configFloat(cfg, "login.retry.initial_backoff_ms"); v > 0 {
		backoff.Initial = time.Duration(v) * time.Millisecond
	}
	if v := configFloat(cfg, "login.retry.max_backoff_ms"); v > 0 {
		backoff.Max = time.Duration(v) * time.Millisecond
	}
	opts = append(opts, retry.WithBackoff(backoff), retry.WithJitter(retry.FullJitter))
	r := retry.New(opts...)
	baldlog.Info(context.Background(), "login retrier enabled",
		"initial_backoff_ms", backoff.Initial.Milliseconds(), "max_backoff_ms", backoff.Max.Milliseconds())
	return r
}

// buildTokenStore 构造令牌存储（Wave 1d）。
//
// 复用 bootstrap 已装配的 Redis 客户端（RedisClient，由 BeforeStart 的
// cache.redis 段构造）。无 Redis 时返回 nil——**禁用态**：
//   - Logout 记日志不吊销（无状态 JWT 无法收回）；
//   - Login 不签发 refresh_token（无处可查的刷新令牌没有意义）；
//   - ValidateToken 退化为纯验签（无吊销检查）。
//
// 这三条降级都在各自调用点显式处理，不静默。
func buildTokenStore() token.Store {
	if bootstrappkg.RedisClient == nil {
		return nil
	}
	return token.NewRedisStore(bootstrappkg.RedisClient)
}

// configFloat 从配置树取数值键（Store 无 GetFloat，经 Get + 类型断言；
// yaml 解析的整数会落为 int，故两种数值类型都接受）。
func configFloat(cfg *baldconfig.Store, key string) float64 {
	v, ok := cfg.Get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func storageRegistry() *baldbootstrap.StorageRegistry {
	sr := baldbootstrap.NewStorageRegistry()
	sr.MustRegister("minio", bootstrappkg.StorageProvider)
	// Wave 5.4：注册 s3 后端（契约 storage.s3 段 → bald/oss/s3）。
	// 官方 provider 签名与 bootstrappkg.StorageProvider 结构化兼容，直接注册。
	sr.MustRegister(s3contract.Type, s3contract.Provider)
	return sr
}

// reconAuditors 是 R1-2 协调的「实际态」载体：协调器逐后端 Mount/Unmount，
// 每个后端组件在 Start 时把自己塞入此表、Dispose 时移除，并刷新全局 MultiAuditor。
// 由 ReconcileCtx.Mount/Unmount（底层 A1 组件生命周期 + reconItems 归属表）串行化，
// 自身无需额外加锁——协调器单次执行是串行的，且框架对多协调器按注册序串行。
var reconAuditors = struct {
	mu  sync.Mutex
	set map[string]audit.Auditor
}{set: make(map[string]audit.Auditor)}

// applyAuditors 根据当前后端表重建全局 MultiAuditor（按后端名排序，顺序确定、
// 便于测试与排查；空则退化为 Nop，绝不阻断审计旁路）。
func applyAuditors() {
	reconAuditors.mu.Lock()
	names := make([]string, 0, len(reconAuditors.set))
	for n := range reconAuditors.set {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]audit.Auditor, 0, len(names))
	for _, n := range names {
		list = append(list, reconAuditors.set[n])
	}
	reconAuditors.mu.Unlock()
	if len(list) == 0 {
		audit.SetAuditor(audit.NopAuditor())
		return
	}
	audit.SetAuditor(securityaudit.NewMulti(list...))
}

// auditBackendComponent 是一个后端审计器组件：Mount 即把自身审计器注册进全局
// MultiAuditor，Unmount 即注销并收尾后端自身生命周期。其名 == 后端名
// （log/store/stream），正是协调器用于 diff 的标识，与 ReconcileCtx.Mounted() 一一对应。
type auditBackendComponent struct {
	name  string
	build func() audit.Auditor // 延迟构造：依赖 InitBridges 后的 DB/Redis
	aud   audit.Auditor        // Start 期构造的实例，Dispose 时按需收尾
}

func (c *auditBackendComponent) Name() string { return c.name }

func (c *auditBackendComponent) Start(ctx context.Context) error {
	a := c.build()
	if a == nil {
		return fmt.Errorf("audit backend %q unavailable", c.name)
	}
	c.aud = a
	reconAuditors.mu.Lock()
	reconAuditors.set[c.name] = a
	reconAuditors.mu.Unlock()
	applyAuditors()
	baldlog.Info(ctx, "audit backend mounted", "backend", c.name)
	return nil
}

func (c *auditBackendComponent) Dispose(ctx context.Context) error {
	reconAuditors.mu.Lock()
	delete(reconAuditors.set, c.name)
	reconAuditors.mu.Unlock()
	applyAuditors()
	// 后端自身生命周期收尾：StreamAuditor 停后台 goroutine 并 drain 剩余缓冲
	// （Mount/Unmount 由 ReconcileCtx 串行化，无并发写 c.aud；log/store 无
	// Close 方法则零副作用）。否则 audit.backends 热切换每次泄漏一个 goroutine
	// + 1024 容量 chan，且已入队事件被静默丢弃。
	if c.aud != nil {
		if closer, ok := c.aud.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	baldlog.Info(ctx, "audit backend unmounted", "backend", c.name)
	return nil
}

// buildAuditBackend 构造某后端的审计器（log 始终可用；store/stream 依赖桥接就绪）。
// 桥接未就绪时返回 nil，协调器会跳过该后端（旁路语义，绝不阻断启动）。
func buildAuditBackend(name string) audit.Auditor {
	switch name {
	case "log":
		return securityaudit.New()
	case "store":
		if bootstrappkg.DB != nil {
			return securityaudit.NewStore(bootstrappkg.DB)
		}
	case "stream":
		// D1：底层 client 经 bootstrap.RedisClient 复用（cache/redis 适配器
		// 不暴露 client）；nil 即无 Redis，跳过该后端。
		if bootstrappkg.RedisClient != nil {
			return securityaudit.NewStream(bootstrappkg.RedisClient)
		}
	}
	return nil
}

// reconcileAudit 是 R1-2 期望态协调函数（逐后端粒度，非整体重建）：配置键
// audit.backends 声明「期望的审计后端集合」，框架用 ReconcileCtx 暴露的实际态
// （r.Mounted()，即本协调器已挂载的后端名）做 diff，仅 Mount 新增、Unmount 移除——
// 完整兑现「只更新变动部分」的 K8s controller 语义。部分失败不回滚，下次协调补齐。
//
// 触发时机由框架负责：首次在 loadConfig 基线后、AfterStart 前的启动收敛期；其后
// 每次 OnConfigChange 携带新快照再次调用，实现运行期热切换（如 [store]→[store,stream]）。
func reconcileAudit(ctx context.Context, rctx *appkit.ReconcileCtx) error {
	want := parseAuditBackends(rctx.String("audit.backends"))
	have := rctx.Mounted() // 实际态：本协调器名下已挂载的后端（Mount/Unmount 维护）

	// diff-apply：期望==实际时直接返回（幂等，不抖动、不发多余审计）。
	add, remove := appkit.DiffStrings(want, have)
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}

	baldlog.Info(ctx, "reconcile audit.backends",
		"add", strings.Join(add, ","), "remove", strings.Join(remove, ","))

	// 新增：期望有、实际无 → 逐后端 Mount（底层 A1 组件生命周期 + 重组审计）。
	for _, name := range add {
		comp := &auditBackendComponent{name: name, build: func() audit.Auditor { return buildAuditBackend(name) }}
		if err := rctx.Mount(ctx, comp.Name(), comp); err != nil {
			// 失败不回滚，下次协调按实际态（reconItems）重新 diff 补齐。
			baldlog.Error(ctx, "reconcile mount audit backend failed",
				"backend", name, "error", err)
		}
	}
	// 移除：实际有、期望无 → 逐后端 Unmount。
	for _, name := range remove {
		if err := rctx.Unmount(ctx, name); err != nil {
			baldlog.Error(ctx, "reconcile unmount audit backend failed",
				"backend", name, "error", err)
		}
	}
	return nil
}

// parseAuditBackends 解析逗号/空格分隔的后端列表，过滤非法取值，去重保序。
func parseAuditBackends(s string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		b := strings.TrimSpace(raw)
		if b == "" || (b != "log" && b != "store" && b != "stream") {
			continue
		}
		if _, ok := seen[b]; ok {
			continue
		}
		seen[b] = struct{}{}
		out = append(out, b)
	}
	return out
}

// contains 判断切片是否含某元素。
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

var osExit = func(code int) { os.Exit(code) }

// appRefT 是 AppKit 的线程安全迟到绑定句柄：管理面路由在 appkit.New 之前注册
// （gin 装配先于 app 构造），handler 闭包捕获 appRef、请求期读取最新 App 实例。
type appRefT struct {
	mu  sync.RWMutex
	app *appkit.AppKit
}

func (r *appRefT) set(a *appkit.AppKit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.app = a
}

func (r *appRefT) get() *appkit.AppKit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.app
}

// heartbeatComp 是 M10.2 的演示组件：Start 起周期心跳 goroutine，Dispose 停止并
// 等待退出——与 StreamAuditor 同构的「有 goroutine 要收」组件生命周期范本，
// 经管理面挂载/卸载可端到端观察 A1 的 Start/Dispose 与重组审计。
type heartbeatComp struct {
	name  string
	stop  chan struct{}
	done  chan struct{}
	appFn func() *appkit.AppKit
}

func newHeartbeatComponent(appFn func() *appkit.AppKit) appkit.Component {
	return &heartbeatComp{
		name:  "demo.heartbeat",
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		appFn: appFn,
	}
}

func (h *heartbeatComp) Name() string { return h.name }

func (h *heartbeatComp) Start(_ context.Context) error {
	go func() {
		defer close(h.done)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				baldlog.Info(context.Background(), "component heartbeat",
					"component", h.name, "components", len(h.appFn().ListComponents()))
			}
		}
	}()
	return nil
}

func (h *heartbeatComp) Dispose(_ context.Context) error {
	close(h.stop)
	<-h.done // 等 goroutine 退出：Dispose 后组件对进程无残留影响（时间可组合性）
	return nil
}

// registerAdminRoutes 把管理面路由挂到 router（appRef 迟到绑定见 appRefT）。
func registerAdminRoutes(router *gin.Engine, ref *appRefT, factories map[string]apiserver.ComponentFactory) {
	apiserver.RegisterAdmin(router, ref.get, factories)
}

// lazyAuthn / lazyAuthz 把 bootstrap 包级桥接变量（InitBridges 在 appkit.BeforeStart
// 才赋值）适配为 authn/authz 接口，供 bundle 构造期注入——bundle 是构造期依赖注入，
// 而桥接是运行期装配，lazy 适配器衔接两者时序（请求期读取最新值）。
type lazyAuthn struct{}

func (lazyAuthn) Authenticate(ctx context.Context) (*authn.AuthClaims, error) {
	return bootstrappkg.Authenticator.Authenticate(ctx)
}

func (lazyAuthn) AuthenticateToken(token string) (*authn.AuthClaims, error) {
	return bootstrappkg.Authenticator.AuthenticateToken(token)
}

type lazyAuthz struct{}

func (lazyAuthz) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	return bootstrappkg.Authorizer.Authorize(ctx, subject, object, action)
}

func newGRPCServerOptions() []grpc.ServerOption {
	// M10.1（P10 验证）：gRPC 无公开方法（全部需认证），整条链切 bundle——
	// Error→RequestID→Observability→Authn→Audit→Authz 链序由 bundle 固化，
	// 替代此前手写的 7 段拦截器组装（authnInterceptor/authzInterceptor 闭包删除）。
	// P9 归一化经 bundle.Normalized() 内置于 Authz 与 Audit 两层。
	// T6 修复：Audit 层注入动态转发器（同 gin 侧 ginBundle）——此前未注入时
	// bundle 显式接 Nop，请求审计与认证失败审计（bundle 会把 auditor 注入
	// AuthnInterceptor）均静默失效。
	grpcBundle := bundle.New(
		bundle.Authn(lazyAuthn{}),
		bundle.Authz(lazyAuthz{}),
		bundle.Audit(securityaudit.Global()), // 动态转发：契约轨装配/热切轨切换即时生效
		bundle.Metrics(obmetrics.Recorder("bald/example")),
		bundle.Normalized(), // P9：FullMethod → 与 HTTP 同源的权限点
	)
	return grpcBundle.GRPCChain()
}

// buildGateway 构造 grpc-gateway 第三服务器（U1：gRPC/HTTP 归 WithGRPC/WithHTTP
// 契约装配，gateway 走 WithExtraServers 逃生舱——独立 :8081 与 gin 主面并存，
// 契约 server.http.driver 的网关面模式是「同一端口二选一」，与此不匹配）。
// 仅在注入 gatewayFactory 时挂载（默认构建即挂载，由 init 注入）。
func buildGateway(
	bootstrap *bootstrapv1.BootstrapConfig,
) []transport.Server {
	if gatewayFactory == nil {
		return nil
	}
	// gateway 需连到 gRPC 服务（用其监听地址，须可连接，不能是 :0）。
	// gateway 配置在构造期即与 HTTP 同源绑定：地址走 gatewayAddr()，TLS 直接取主 HTTP 的 http.tls 段，
	// 不再依赖 BeforeStart 运行时回填（消除全局可变态 + 时序耦合）。
	gwHttpCfg := &bootstrapv1.Server_Http{Addr: gatewayAddr()}
	if t := bootstrap.GetServer().GetHttp(); t.GetTls() != nil {
		gwHttpCfg.Tls = t.GetTls()
	}
	gw, err := gatewayFactory(gwHttpCfg, bootstrap.GetServer().GetGrpc())
	if err != nil {
		// 网关构造失败不应静默降级（否则 REST 路由凭空消失），直接 panic（fail-fast）。
		panic("build gateway server: " + err.Error())
	}
	return []transport.Server{gw}
}

// gatewayFactory 构造 grpc-gateway 服务器（REST → gRPC 转码）。M5 默认挂载：
// REST 请求经 registerGateway 转码进入 SecretService，复用同一 gRPC 拦截器链
// （认证/授权/多租户）。
//
// 探针：第三服务器只是对外转码面，刻意不挂 /healthz /readyz——探针归主面
// （:8080 由 appkit.WithHealth 默认装配），就绪状态同源，探主面即可。
var gatewayFactory = func(httpCfg *bootstrapv1.Server_Http, grpcBackend *bootstrapv1.Server_Grpc) (*gateway.GatewayServer, error) {
	return gateway.NewGatewayServer(httpCfg, grpcBackend, registerGateway)
}

// gatewayAddr 读取 gateway 监听地址：env BALD_GATEWAY_ADDR 优先，缺省 :8081，
// 与 HTTP 主服务（http.addr）分开避免端口冲突。
func gatewayAddr() string {
	if v := os.Getenv("BALD_GATEWAY_ADDR"); v != "" {
		return v
	}
	return ":8081"
}

// registerGateway 把 grpc-gateway 的 HTTP handler 注册到 runtime.ServeMux 并交回
// http.Handler（transport.NewGatewayServer 依赖倒置，核心不依赖 grpc-gateway）。
// conn 由 GatewayServer 内部建立（指向本进程 gRPC 服务）。T2 起挂载 tenant/user 网关。
func registerGateway(ctx context.Context, conn *grpc.ClientConn) (http.Handler, error) {
	mux := runtime.NewServeMux()
	if err := adminv1.RegisterSecretServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := tenantv1.RegisterTenantServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := userv1.RegisterUserServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := menuv1.RegisterMenuServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := permissionv1.RegisterPermissionServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := dictv1.RegisterDictTypeServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := dictv1.RegisterDictEntryServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := filev1.RegisterFileServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := auditv1.RegisterAuditServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	return mux, nil
}

func setLogger(opts *bslog.Options) {
	baldlog.SetLogger(bslog.New(opts,
		bslog.WithFilter(bslog.FilterKey("password")),
		bslog.WithFilter(bslog.FilterKey("token")),
		bslog.WithAttrs(slog.String("service.name", "go-bald-admin")),
	))
	// M7 审计后端注入（落库版）在 InitBridges 之后装配（见 appkit.BeforeStart），
	// 因需 bootstrap.DB 已建立；此处仅设 Logger，不再提前注入 Auditor。
}
