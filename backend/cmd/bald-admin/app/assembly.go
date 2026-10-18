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
	"github.com/kalandramo/bald-admin/internal/apiserver"
	tenantd "github.com/kalandramo/bald-admin/internal/apiserver/domains/tenantd"
	userd "github.com/kalandramo/bald-admin/internal/apiserver/domains/userd"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
	"github.com/kalandramo/bald-admin/internal/security/token"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	obmetrics "github.com/kalandramo/bald/contrib/observability-otlp/metrics"
	storegorm "github.com/kalandramo/bald/contrib/store-gorm"
	"github.com/kalandramo/bald/health"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/log/bslog"
	miniocontract "github.com/kalandramo/bald/oss/minio/contract"
	s3contract "github.com/kalandramo/bald/oss/s3/contract"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/appkit/mech"
	"github.com/kalandramo/bald/pkg/appkit/std"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/middleware/bundle"
	"github.com/kalandramo/bald/pkg/store"
	nacoscontract "github.com/kalandramo/bald/registry/nacos/contract"
	"github.com/kalandramo/bald/transport/asynq"
	asynqcontract "github.com/kalandramo/bald/transport/asynq/contract"
	"github.com/kalandramo/bald/transport/cron"
	croncontract "github.com/kalandramo/bald/transport/cron/contract"
	gatewaycontract "github.com/kalandramo/bald/transport/gateway/contract"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
)

// serveRunE 是根命令的 RunE——bald-admin 的**启动主流程**。
//
// 批 2 单一装配入口：契约装载、--config 解析（默认路径经 appkit.ConfigFile 声明，
// 原 preloadBootstrap 手写扫描已内化）、约定装配（Bind×3、装载/校验/日志两阶段、
// 热更新、段驱动 Provider、信号处理与 Run）全部在 Serve 内部；业务只经
// appOptions 声明配置表达不了的能力。失败记日志后以非零码退出（MustServe）。
func serveRunE(_ *cobra.Command, _ []string) error {
	appkit.MustServe(context.Background(), appOptions(options.NewServerOptions())...)
	return nil
}

// appOptions 声明业务能力（Serve 的约定装配消费这些 Option）。
//
// 抽成函数是为了让 e2e 测试能复用**同一份**声明（appkit.Serve 驱动），
// 而不是在测试里另抄一份——复制出来的应用与真实运行的不一致，测试就失去
// 回归价值（历史教训见 assembly_e2e_test.go 头注）。
//
// Serve 内化的约定（原本要手写的样板）：
//   - 契约装载：bconf.NewBootstrap 骨架 + --config（默认 configs/bald-admin.yaml，
//     来自 ConfigFile）的文件/env 基线预装载；
//   - Bind server.http/server.grpc/--log.* 三组框架 flag + 业务 Bind 声明；
//   - BeforeStart：Settings→Unmarshal→Validate→按契约 logger 段重建 Logger；
//   - OnConfigChange：热更新副本试装载+校验后原子落盘，并重建 Logger；
//   - app 元数据（name/version/env/stop_timeout）取自契约 app 段；
//   - 停机恢复启动前 Logger、释放配置层资源（Effect 效应账本）。
//
// 业务保留的声明（配置驱动不了）：路由、gRPC service、拦截器链序、
// 探针依赖、日志脱敏、审计协调、各段业务语义 Provider——见下方各 Option。
func appOptions(svrOpts *options.ServerOptions) []appkit.Option {
	// biz 在 BeforeStart 内构造——InitializeBiz 依赖 InitBridges 建立的
	// store / Redis，构造期构造会固化 nil（时序倒置）。框架保证业务 BeforeStart
	// 先于 server 构造钩子执行（注册序=执行序），故 registerGRPC 被调用时
	// bizSet 已就绪。
	//
	// err 与 bizSet 都在**函数作用域**声明：闭包内须用 `=` 赋值（用 `:=` 会在
	// 闭包块内新建同名变量，外层 bizSet 仍为 nil——registerGRPC 读到的即 nil）。
	var (
		bizSet *apiserver.BizSet
		// authenticator/authorizer 提升到函数作用域——它们在 BeforeStart 内构造
		//（依赖 InitBridges 建立的 store/Redis），再供两个**运行期**消费者读取：
		// appkit.WithGRPCOptions 的选项工厂与 sseServerProvider 的授权钩子。
		authenticator authn.Authenticator
		authorizer    authz.Authorizer
		// repos 同样提升到函数作用域：InitBridges 返回值原为 BeforeStart 闭包局部，
		// 批 4 起域清单的 Deps getter（下方 opts 内）也要读它。闭包内赋值用 `=`
		//（`:=` 会新建同名变量，外层仍为 nil——同 bizSet 的纪律）。
		repos *bootstrappkg.Repositories
	)

	// 管理面：运行期组件观测与热插拔（工厂目录由业务定义——核心只管挂载原语）。
	// demo.heartbeat 演示带 goroutine 的组件生命周期（Start 起心跳、Dispose 收），与
	// StreamAuditor 同构；admin 角色经 /admin/components 端点挂载/卸载。
	appRef := &appRefT{}
	componentFactories := map[string]apiserver.ComponentFactory{
		"demo.heartbeat": func() appkit.Component { return newHeartbeatComponent(appRef.get) },
	}

	// 健康检查聚合器：HTTP /healthz /readyz 与 gRPC 标准健康服务状态同源，由
	// appkit.WithHealth 默认装配（探针路由归装配层，协议实现不注册任何路由）。
	// 本范例尚无依赖项要检查，空聚合器=恒就绪（等价于迁移前的 ready 桩）。
	healthChecker := health.New()

	// gin 引擎：构造期只建伺服面骨架（无中间件）。全局链在 BeforeStart 挂载——
	// 批 2 起 Serve 内部持有契约，声明期读不到契约中间件段；Run 期挂载还使
	// 中间件配置吃到完整四源终值（flag/env/文件/远程；此前只有预装载基线）。
	// gin 的 Use 只影响其后注册的路由，故 BeforeStart 内先挂链再 RegisterRoutes。
	router := gin.New()

	// 装载后的契约快照（BeforeStart 解码自 app.Settings()；AfterStart 读地址
	// 打日志用——与原预装载契约同源同值）。
	var loadedContract bootstrapv1.BootstrapConfig

	// 契约中间件段的资源释放（当前仅 rate_limit 的限流器需要 Close）。挂停机
	// Effect：注册在框架 Effect 之后，逆序回放时**先于**框架底座执行（业务资源
	// 先收，与 mech.Effect 的顺序约定一致）。实际 close 在 BeforeStart 内赋值，
	// 无中间件资源时保持 no-op。
	mwClose := func() error { return nil }

	// gRPC service 注册回调捕获 BeforeStart 装配的 biz（全部 service 需 biz 注入）。
	// secret 的 DeleteSecret 同经 biz 真实删除（gRPC 直连与 gateway 转码共用）。
	registerGRPC := func(s *grpc.Server) {
		adminv1.RegisterSecretServiceServer(s, secretgrpc.NewServer(bizSet.Secret))
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

	opts := []appkit.Option{
		// --- 框架自有后端 + 第三方 Provider（批 2 目标形态，评估 §4.1/§4.2）---
		// std.Standard()：框架自有后端的段驱动 Provider 集合（http/grpc 服务器
		// 构造、audit.log 默认——对账表见 pkg/appkit/std 包文档）。
		std.Standard(),
		// App 实例绑定：管理面 handler 与 BeforeStart 经 appRef 迟到取 App。
		// app 段被契约预填恒存在，本 Provider 无条件触发（见 providers.go）。
		appBindingProvider{bindApp: appRef.set},
		// storegorm.AppProvider()：契约 database.sql 段 → *gorm.DB，产物经
		// app.Resource("database.sql") 取用，停机自动释放连接池。
		storegorm.AppProvider(),
		// 配置源（config 段：nacos 远程层）与注册中心（registry.nacos 段）。
		configSourceProvider{},
		nacoscontract.AppProvider(),
		// cache.redis 段：业务桥接 Provider（env/降级/RedisClient 语义，见
		// providers.go 的取舍说明）。cache 段未配置时引擎跳过（合法）。
		cacheRedisProvider{},
		// 对象存储两后端（storage.minio / storage.s3 段，互斥择一）。
		miniocontract.AppProvider(),
		s3contract.AppProvider(),
		// 可观测性：tracer/metrics 段经 observability-otlp 函数契约缝分派
		// （原 Tracer/MetricsRegistry 注册表形态删除，type 分派收敛到 Provider）。
		tracerProvider{},
		metricsProvider{},
		// 审计段：业务 Provider 接管 std 默认（末者胜），装配 log/store/stream
		// 三后端——见 audit_wiring.go。
		auditProvider{},

		// --- server 协议域第三方面（契约段驱动；段缺失即跳过）---
		// asynq：server.asynq 段。任务 handler 注册与 SetScheduler 接线在回调内
		// 完成（需 bizSet，故经 getter 传入）；段未配 redis_address / codec 时经
		// resolver 回退（保「配 cache.redis 即启用 asynq」与 env
		// BALD_ADMIN_ASYNQ_CODEC 的既有兼容）。
		asynqcontract.AppProvider(
			asynqcontract.WithCodecRegistration(registerAsynqCodecs),
			asynqcontract.WithAddressResolver(bootstrappkg.ResolveRedisAddr),
			asynqcontract.WithCodecResolver(func() string { return svrOpts.Asynq.Codec }),
			asynqcontract.WithHandlers(func(ctx context.Context, s *asynq.Server) error {
				if err := registerAsynqHandlers(ctx, s); err != nil {
					return err
				}
				// biz 接线：把 asynq server 适配为 task.Scheduler 注入 biz。
				if bizSet != nil && bizSet.Task != nil {
					bizSet.Task.SetScheduler(newAsynqScheduler(s))
				}
				return nil
			}),
		),
		// cron：server.cron 段。周期任务注册在回调内完成。
		croncontract.AppProvider(croncontract.WithJobs(func(s *cron.Server) error {
			return registerCronJobs(context.Background(), s)
		})),
		// gateway：server.gateway 段。转码注册回调复用本包的 registerGateway
		//（与 HTTP 面同源）。
		gatewaycontract.AppProvider(registerGateway),
		// SSE：server.sse 段。授权钩子与 message 域推流接线经 Option 注入
		//（见 sse.go）；段缺失即跳过——「不启用」= 移除 sse 段。
		sseServerProvider(func() authn.Authenticator { return authenticator }, func() *apiserver.BizSet { return bizSet }),

		// --- 业务域声明（域形态，批 4 起：tenant/user 先行，其余 19 域随批跟进）---
		// 手写域清单（bald-admin 无 domains.gen.go，W3 生成物落地前的清单承载
		// 形态——与上方 Provider 清单并列；Domain 实现 Option，变参直传 MustServe）。
		// 基础设施取用约定：store/authn/authz 住装配层（InitBridges 与 BeforeStart
		// 构造），域经 Deps getter 迟到取用——getter 读本函数作用域变量（闭包内
		// `=` 赋值纪律同 bizSet）；域 Wire 晚于业务 BeforeStart 执行（bald/pkg/
		// appkit/domain.go 时序窗口），getter 届时返回已就绪实例。
		tenantd.Domain(tenantd.Deps{
			Store: func() *store.Store[authmodel.Tenant] { return repos.Tenant },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),
		userd.Domain(userd.Deps{
			Store: func() *store.Store[authmodel.User] { return repos.User },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),

		// --- 配置命名空间与本地文件 ---
		// env 前缀（BALD_ADMIN_*）由契约 app.name 派生（B6 两遍合并）——
		// configs/bald-admin.yaml 的 app.name 即其来源（缺失启动 fail-fast）。
		// 本地配置文件 + fsnotify 热更新。显式路径缺失 → fail-fast（配置携带
		// DB/Redis 凭据，宁可启动即失败也不要半配置运行）。--config flag 可覆盖，
		// 由 Serve 内部 parseConfigFlag 解析（原 preloadBootstrap 手写扫描已内化）。
		appkit.ConfigFile(configFileDefault),
		appkit.WatchConfigFile(true),
		// 严格契约校验：业务段（biz）整棵放行，其余顶层段内的未知键 fail-fast。
		// 白名单由本应用给出（options.BusinessSection）——框架不认识 biz。
		appkit.StrictContract(options.BusinessSection),
		// 业务配置 flag 通道（--biz.login.rate_limit.rate=9 等）。缺此注册时业务
		// flag 进不了装载 FlagSet，「flag > env > 文件」对业务配置项整条失效。
		appkit.Bind("", svrOpts),

		// --- 能力声明（代码提供）---
		appkit.WithHTTP(router),
		// 拦截器链改**运行期求值**——工厂在 Run 期 server 构造点调用（业务
		// BeforeStart 之后），可读上方运行期赋值的 authenticator/authorizer。
		appkit.WithGRPC(registerGRPC),
		appkit.WithGRPCOptions(func() []grpc.ServerOption {
			return newGRPCServerOptions(authenticator, authorizer)
		}),
		// 健康检查默认装配：HTTP 双探针 + gRPC health 状态联动。
		appkit.WithHealth(healthChecker),

		// 日志装饰（B7：WithLogDecorators 取消导出，装饰归 provider 自持）：
		// 经本应用的 loggerProvider（覆盖 std 默认）注入。两套脱敏机制
		// **互补，不是重复**（实测验证）：
		//   - 契约 `logger.filter_keys` 包在 MultiLogger 外层，覆盖**位置参数对**
		//     （"password", v）与 **ctx 属性流**；
		//   - bslog.WithFilter 是 slog handler 层装饰器，覆盖 **slog.Attr** 形式
		//     （slog.String("password", v)）。契约版不处理 Attr——会被漏过。
		// 二者缺一即有来源未被掩码，故并存。
		loggerProvider{deco: []bslog.Option{
			bslog.WithFilter(bslog.FilterKey("password")),
			bslog.WithFilter(bslog.FilterKey("token")),
			bslog.WithAttrs(slog.String("service.name", "bald-admin")),
		}},

		// --- 机制层（原 appkit.WithX 声明，批 2 下沉 mech 子包）---
		// 能力声明（启动期 fail-fast）：BeforeStart 的 InitBridges 将建立真实 DB
		// 连接，审计落库（StoreAuditor）依赖它。
		mech.Provides("db"),
		mech.Requires("audit.store", "db"),

		// 增量协调（key 级订阅）：仅当 http.addr 实际变化才触发（同值刷新、
		// 其他 key 变更均不波及），与全量 reload 互补。
		mech.OnKeyChange("server.http.addr", func(old, new string) {
			baldlog.Info(context.Background(), "server.http.addr changed",
				"old", old, "new", new)
		}),

		// 契约中间件段的资源释放（当前仅 rate_limit 的限流器需要 Close）。
		mech.Effect("http-middleware", func(context.Context) error {
			return mwClose()
		}),

		// --- 业务 BeforeStart（装载链之后执行——契约终值可读、阶段 B Provider
		// 产物已入资源面）---
		appkit.BeforeStart(func(ctx context.Context) error {
			// App 实例由自定义 Provider 在阶段 B 绑定（见 providers.go bindApp）——
			// 此刻必已就绪；nil 属装配缺陷，显式失败不静默。
			app := appRef.get()
			if app == nil {
				return fmt.Errorf("app not bound by segment providers (audit/tracer/metrics/cache all absent?)")
			}

			// 装载后的业务配置快照解码进 ServerOptions（覆盖默认值）并校验。
			if err := svrOpts.DecodeInto(app.Settings()); err != nil {
				return err
			}
			if err := svrOpts.Validate(); err != nil {
				return err
			}

			// 合并终值快照 → 契约（AfterStart 地址日志、bootstrappkg.Configure 的
			// 依赖段来源）。原实现读预装载契约——批 2 起契约归 Serve 内部，业务侧
			// 经 Settings() 解码等价重建（含 flag/env/远程全源终值，较此前只读
			// 预装载基线更完整）。
			if err := baldconfig.Unmarshal(app.Settings(), &loadedContract); err != nil {
				return fmt.Errorf("decode merged settings into bootstrap contract: %w", err)
			}

			// 契约中间件段（server.http.middleware.*）→ bundle 全局链。先挂链再注册
			// 路由（gin 的 Use 只影响其后注册的路由）。非法段值在此 fail-fast（先于
			// 监听，与原构造期校验同为启动期错误，且现在吃到四源终值）。
			mwOpts, close, err := bundle.FromMiddleware(
				loadedContract.GetServer().GetHttp().GetMiddleware(),
			)
			if err != nil {
				return fmt.Errorf("bundle: build http middleware from config: %w", err)
			}
			mwClose = close
			ginBundle := bundle.New(append([]bundle.Option{
				bundle.Audit(securityaudit.Global()), // 动态转发：装配/热切对已挂中间件即时生效
				bundle.Metrics(obmetrics.Recorder("bald/example")),
				bundle.Normalized(), // 归一化：审计 object/action 与 gRPC 同源
			}, mwOpts...)...)
			router.Use(ginBundle.Gin()...)

			// 契约装配实例注入桥接变量（Provider 产物；nil 实例 = 降级态，Wire* 对
			// nil 为 no-op，InitBridges 走自建/降级）。资源按契约段路径取用
			// （App.Resource(segment)，any+断言——类型安全在消费侧）。
			if v, ok := app.Resource("database.sql"); ok {
				bootstrappkg.WireDatabase(v)
			}
			if v, ok := app.Resource("cache.redis"); ok {
				bootstrappkg.WireCache(v)
			}
			if v, ok := app.Resource("storage.minio"); ok {
				bootstrappkg.WireStorage(v)
			}
			// storage.type=s3 时装配 s3 后端（与 minio 互斥择一）。
			// WireStorage 按实际类型分派，两者共用 ObjectStorageBridge。
			if v, ok := app.Resource("storage.s3"); ok {
				bootstrappkg.WireStorage(v)
			}
			// 注入真实依赖配置（业务自持 file.bucket；database/cache/storage
			// 段已由 Provider 消费，此处仅传桥接所需的余下配置）。
			bootstrappkg.Configure(&loadedContract, svrOpts.File.Bucket)
			// 审计兜底租户（可选，缺省 t-default）：login 失败/permission/
			// data_access 三类事件天然无租户，落库时兜底到此值以保证在租户
			// 隔离下仍可见（见 internal/security/audit/record_mapper.go）。
			// **必须在 audit store 构造前调用**——映射在构造期求值。
			securityaudit.SetFallbackTenant(svrOpts.Audit.FallbackTenant)
			// 在 bootstrap 包内装配 bald 桥接（注入 Authenticator / Authorizer /
			// store.RegisterTenant / store.RegisterDataScope）。
			// 仓储经返回值显式取出（不再写包级变量）。
			repos, err = bootstrappkg.InitBridges(ctx)
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
			// 时序保证：本钩子（业务 BeforeStart）**先于**框架 server 构造钩子执行
			//（注册序=执行序）——故 registerGRPC 被调用时 bizSet 已就绪，
			// WithHTTP(router) 的 handler 也已挂好全部路由。
			biz, err := InitializeBiz(repos)
			if err != nil {
				return fmt.Errorf("initialize biz (wire): %w", err)
			}
			bizSet = biz
			// gin 路由注册。认证器/授权器为**真实实例**（上方已构造），
			// 中间件直接持有——无请求期解析层。
			apiserver.RegisterRoutes(router, authenticator, authorizer, bizSet)
			// 管理面：运行期组件观测与热插拔（appRef 在 provider Build 期绑定）。
			registerAdminRoutes(router, appRef, authenticator, authorizer, componentFactories)
			// 文件存储运行期接线。
			//
			// 此处已非必需：InitializeBiz 现于 InitBridges **之后**执行
			//（上方），wire 构造 file biz 时 bootstrap.MinioStorage 已就绪，
			// 「构造期值拷贝拿到 nil」的前提消失。保留为**幂等补注**——降级路径
			//（storage 段未配置 → ObjectStorageBridge/MinioStorage 均 nil）下
			// 两个分支都不写入，biz 内判 nil 返回明确错误，语义不变。
			// 注 ObjectStorageBridge（按 storage.type 已包成 minio/s3
			// 适配器）——minio 与 s3 走同一条接线，签名差异由适配层吸收。
			if bootstrappkg.ObjectStorageBridge != nil {
				bizSet.File.SetObjectStorage(bootstrappkg.ObjectStorageBridge, bootstrappkg.FileBucket)
			} else {
				bizSet.File.SetStorage(bootstrappkg.MinioStorage, bootstrappkg.FileBucket)
			}
			// 同款时序：secret/dict 的 Cache-Aside 接入配置驱动的 RedisCache——
			// cache.redis 段（含 password/db）只流向 bootstrap.RedisCache；wire 的
			// env 通道只带地址、拿不到完整参数；不接线则配置驱动运行下缓存静默失效。
			// 未配置段时 RedisCache 为 nil，SetCache 不覆盖，保留 wire env 通道
			//（CI 覆盖手段）。SetCache 接收通用 KV 适配器（cache.Cache），biz 内部
			// 包装为 loadable 读穿透缓存（loader 构造期绑定，从 key 反解业务参数）。
			if bootstrappkg.RedisCache != nil {
				bizSet.Secret.SetCache(bootstrappkg.RedisCache)
				bizSet.Dict.SetCache(bootstrappkg.RedisCache)
			}
			// 登录限流接线（业务自持配置段 login.rate_limit.*）。框架契约的
			// server.http.rate_limit 段是**中间件级**限流且当前零消费者，故业务级
			// 登录限流走自持段。未配置时保持 nil = 禁用态（不阻断登录，fail-open）。
			if lim := buildLoginLimiter(svrOpts); lim != nil {
				bizSet.Auth.SetLoginLimiter(lim)
			}
			// 登录 DB 查询熔断接线（业务自持配置段 login.breaker.*）。
			if cb := buildLoginBreaker(svrOpts); cb != nil {
				bizSet.Auth.SetLoginBreaker(cb)
			}
			// 登录 DB 查询重试接线（业务自持配置段 login.retry.*）。
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
		mech.Reconcile("audit.backends", reconcileAudit),

		appkit.AfterStart(func(ctx context.Context) error {
			// 地址为契约终值（BeforeStart 解码自合并快照；:0 动态端口场景见注册
			// 中心聚合结果）。
			ctx = baldlog.ContextWithAttrs(ctx,
				slog.String("stage", "started"),
				slog.String("grpc", loadedContract.GetServer().GetGrpc().GetAddr()))
			baldlog.Info(ctx, "bald-admin started",
				"http", loadedContract.GetServer().GetHttp().GetAddr())
			return nil
		}),
		appkit.BeforeStop(func(ctx context.Context) error {
			baldlog.Info(ctx, "bald-admin stopping")
			return nil
		}),
	}

	return opts
}

// NewCommand 构造 bald-admin 根命令。
//
// **flag 的注册不在 cobra 层**——root 不注册任何 flag（无 Flags()/PersistentFlags()
// 调用）。全部 flag 由 Serve 路径的 loadConfig 自建 pflag.FlagSet 注册，两类来源
// 合流：
//   - 框架：Serve 硬编码绑定 `--server.http.*` / `--server.grpc.*` 与 `--log.*`
//     壳（appkit 装配），外加 --config（parseConfigFlag）；
//   - 业务：本包的 appkit.Bind("", svrOpts) → ServerOptions.AddFlags，
//     注册 `--biz.login.rate_limit.rate` 等业务项。
//
// 二者经同一个装载 FlagSet 合流，共享「flag > env > 文件 > 远程」优先级。
//
// 故 cobra 只需放行未知 flag（FParseErrWhitelist.UnknownFlags）——否则它解析
// os.Args 时会因不认识 `--config` / `--server.http.addr` / `--biz.*` 而提前
// 报错退出。cobra 在这里只负责「识别子命令 + 放行参数」，不参与 flag 定义。
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
