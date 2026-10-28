package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	filestore "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file/filestore"
	auditlogd "github.com/kalandramo/bald-admin/internal/apiserver/domains/auditlogd"
	authd "github.com/kalandramo/bald-admin/internal/apiserver/domains/authd"
	cachemonitord "github.com/kalandramo/bald-admin/internal/apiserver/domains/cachemonitord"
	dashboardd "github.com/kalandramo/bald-admin/internal/apiserver/domains/dashboardd"
	dictd "github.com/kalandramo/bald-admin/internal/apiserver/domains/dictd"
	identityd "github.com/kalandramo/bald-admin/internal/apiserver/domains/identityd"
	filed "github.com/kalandramo/bald-admin/internal/apiserver/domains/filed"
	languaged "github.com/kalandramo/bald-admin/internal/apiserver/domains/languaged"
	menud "github.com/kalandramo/bald-admin/internal/apiserver/domains/menud"
	messaged "github.com/kalandramo/bald-admin/internal/apiserver/domains/messaged"
	mfad "github.com/kalandramo/bald-admin/internal/apiserver/domains/mfad"
	orgd "github.com/kalandramo/bald-admin/internal/apiserver/domains/orgd"
	permissiond "github.com/kalandramo/bald-admin/internal/apiserver/domains/permissiond"
	permgroupd "github.com/kalandramo/bald-admin/internal/apiserver/domains/permgroupd"
	pland "github.com/kalandramo/bald-admin/internal/apiserver/domains/pland"
	portald "github.com/kalandramo/bald-admin/internal/apiserver/domains/portald"
	secretd "github.com/kalandramo/bald-admin/internal/apiserver/domains/secretd"
	taskd "github.com/kalandramo/bald-admin/internal/apiserver/domains/taskd"
	tenantd "github.com/kalandramo/bald-admin/internal/apiserver/domains/tenantd"
	userd "github.com/kalandramo/bald-admin/internal/apiserver/domains/userd"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	appauthz "github.com/kalandramo/bald-admin/internal/security/authz"
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
	"github.com/kalandramo/bald-admin/internal/security/token"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	"github.com/kalandramo/bald/cache"
	authnjwt "github.com/kalandramo/bald/contrib/authn-jwt"
	obmetrics "github.com/kalandramo/bald/contrib/observability-otlp/metrics"
	storegorm "github.com/kalandramo/bald/contrib/store-gorm"
	"github.com/kalandramo/bald/health"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/log/bslog"
	miniooss "github.com/kalandramo/bald/oss/minio"
	miniocontract "github.com/kalandramo/bald/oss/minio/contract"
	s3contract "github.com/kalandramo/bald/oss/s3/contract"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/appkit/appcli"
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
)

// serveRunE 是根命令的 RunE——bald-admin 的**启动主流程**。
//
// 单一装配入口：契约装载、--config 解析（默认路径经 appkit.ConfigFile 声明，
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
	// 业务对象构造归各域声明包的 Wire，本函数
	// 只声明域清单与依赖——运行期产物经下方函数作用域变量 + Deps getter
	// 迟到取用。闭包内赋值用 `=`（`:=` 会新建同名变量，外层仍为 nil——getter
	// 闭包读不到的旧坑，纪律保留）。
	var (
		// authenticator/authorizer 提升到函数作用域——它们在 BeforeStart 内构造
		//（依赖 InitBridges 建立的 store/Redis），再供两个**运行期**消费者读取：
		// appkit.WithGRPCOptions 的选项工厂与 sseServerProvider 的授权钩子。
		authenticator authn.Authenticator
		authorizer    authz.Authorizer
		// repos 同样提升到函数作用域：InitBridges 在 BeforeStart 闭包内构造，
		// 域清单的 Deps getter（下方 opts 内）也要读它。闭包内赋值用 `=`
		//（`:=` 会新建同名变量，外层仍为 nil——同上纪律）。
		repos *bootstrappkg.Repositories
		// bizCache 供 secret/dict 两域的 Deps.Cache getter 读取：BeforeStart 内
		// 解析「配置驱动 RedisCache 优先、env 兼容通道兜底」（赋值点见 BeforeStart，
		// 域 Wire 晚于其执行，getter 届时读到已抉择的实例）。
		bizCache cache.Cache
		// tokenStore/captchaStore/mfaChallenges 供 authd/mfad 域的 Deps getter
		// 读取：BeforeStart 内随 InitBridges 的 Redis 抉择（复用同一 Redis 实例；
		// 无 Redis 时保持 nil——authd/mfad 域的 setter 对 nil 不覆盖 = 禁用态）。
		tokenStore    token.Store
		captchaStore  captcha.Store
		mfaChallenges mfa.ChallengeStore
	)

	// 管理面：运行期组件观测与热插拔（工厂目录由业务定义——核心只管挂载原语）。
	// demo.heartbeat 演示带 goroutine 的组件生命周期（Start 起心跳、Dispose 收），与
	// StreamAuditor 同构；admin 角色经 /admin/components 端点挂载/卸载。
	appRef := &appRefT{}
	componentFactories := map[string]hgin.ComponentFactory{
		"demo.heartbeat": func() appkit.Component { return newHeartbeatComponent(appRef.get) },
	}

	// 健康检查聚合器：HTTP /healthz /readyz 与 gRPC 标准健康服务状态同源，由
	// appkit.WithHealth 默认装配（探针路由归装配层，协议实现不注册任何路由）。
	// 本范例尚无依赖项要检查，空聚合器=恒就绪。
	healthChecker := health.New()

	// gin 引擎：构造期只建伺服面骨架（无中间件）。全局链在 BeforeStart 挂载——
	// Serve 内部持有契约，声明期读不到契约中间件段；Run 期挂载还使
	// 中间件配置吃到完整四源终值（flag/env/文件/远程）。
	// gin 的 Use 只影响其后注册的路由，故 BeforeStart 内先挂链再注册路由（域 Routes，注册序=执行序）。
	router := gin.New()

	// 装载后的契约快照（BeforeStart 解码自 app.Settings()；AfterStart 读地址
	// 打日志用）。
	var loadedContract bootstrapv1.BootstrapConfig

	// 契约中间件段的资源释放（当前仅 rate_limit 的限流器需要 Close）。挂停机
	// Effect：注册在框架 Effect 之后，逆序回放时**先于**框架底座执行（业务资源
	// 先收，与 mech.Effect 的顺序约定一致）。实际 close 在 BeforeStart 内赋值，
	// 无中间件资源时保持 no-op。
	mwClose := func() error { return nil }

	// gRPC service 注册：全部
	// service 登记住 11 个带 proto 面的域声明包（tenant/user/secret/menu/
	// permission/dict×2/file/auditlog/org×2）的 Services 槽位；业务零直接登记，
	// 业务侧不传 gRPC 注册回调——appkit.composeDomainServices 对「无业务回调
	// 而域有 Services」合成纯域回调，std server Provider 据此照常构建 gRPC 面，
	// 拦截器链经 WithGRPCOptions（下方，业务声明保留）照常生效。gateway 转码
	//（registerGateway）是独立声明，不受影响。
	opts := []appkit.Option{
		// --- 框架自有后端 + 第三方 Provider ---
		// std.Standard()：框架自有后端的段驱动 Provider 集合（http/grpc 服务器
		// 构造、audit.log 默认——对账表见 pkg/appkit/std 包文档）。
		std.Standard(),
		// App 实例绑定：管理面 handler 与 BeforeStart 经 appRef 迟到取 App。
		// app 段被契约预填恒存在，本 Provider 无条件触发（见 providers.go）。
		appBindingProvider{bindApp: appRef.set},
		// storegorm.AppProvider()：契约 database.sql 段 → *gorm.DB，产物经
		// storegorm.SQL.Resource(app) 取用，停机自动释放连接池。
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
		// 完成（task 域 biz 经 taskd.Biz() 访问器迟到读取——回调时点（迟后阶段
		// server 构建）晚于域 Wire）；段未配 redis_address / codec 时经
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
				// biz 接线：把 asynq server 适配为 task.Scheduler 注入 task 域 biz
				//（同一实例，无双构造；nil=域未声明，防御性跳过）。
				if b := taskd.Biz(); b != nil {
					b.SetScheduler(newAsynqScheduler(s))
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
		// SSE：server.sse 段。授权钩子经 Option 注入；message 域推流接线在
		// 回调内经 messaged.Biz() 迟到读取（见 sse.go）；段缺失即跳过——
		// 「不启用」= 移除 sse 段。
		sseServerProvider(func() authn.Authenticator { return authenticator }),

		// --- 业务域声明（21 域全量在册）---
		// 手写域清单（bald-admin 无 domains.gen.go，与上方 Provider 清单并列——
		// Domain 实现 Option，变参直传 MustServe）。
		// 基础设施取用约定：store/authn/authz 住装配层（InitBridges 与 BeforeStart
		// 构造），域经 Deps getter 迟到取用——getter 读本函数作用域变量（闭包内
		// `=` 赋值纪律）；域 Wire 晚于业务 BeforeStart 执行（bald/pkg/
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
		// --- 机械域：secret/menu/permission/dict/file/
		// auditlog/org/language）---getter 形态同上：store 读 repos（InitBridges
		// 产物），authn/authz 读 BeforeStart 构造的函数作用域变量；secret/dict
		// 另读 bizCache（下方 BeforeStart 抉择），file 另读 bootstrap 的存储
		// 桥接三件（WireStorage/Configure 产物）。
		secretd.Domain(secretd.Deps{
			Store: func() *store.Store[authmodel.Secret] { return repos.Secret },
			Cache: func() cache.Cache { return bizCache },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),
		menud.Domain(menud.Deps{
			Store: func() *store.Store[authmodel.Menu] { return repos.Menu },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),
		permissiond.Domain(permissiond.Deps{
			Store:       func() *store.Store[authmodel.Permission] { return repos.Permission },
			PolicyStore: func() *store.Store[authmodel.RolePolicy] { return repos.RolePolicy },
			Authn:       func() authn.Authenticator { return authenticator },
			Authz:       func() authz.Authorizer { return authorizer },
		}),
		dictd.Domain(dictd.Deps{
			TypeStore:  func() *store.Store[authmodel.DictType] { return repos.DictType },
			EntryStore: func() *store.Store[authmodel.DictEntry] { return repos.DictEntry },
			Cache:      func() cache.Cache { return bizCache },
			Authn:      func() authn.Authenticator { return authenticator },
			Authz:      func() authz.Authorizer { return authorizer },
		}),
		filed.Domain(filed.Deps{
			Store:         func() *store.Store[authmodel.File] { return repos.File },
			ObjectStorage: func() filestore.ObjectStorage { return bootstrappkg.ObjectStorageBridge },
			Minio:         func() *miniooss.Storage { return bootstrappkg.MinioStorage },
			Bucket:        func() string { return bootstrappkg.FileBucket },
			Authn:         func() authn.Authenticator { return authenticator },
			Authz:         func() authz.Authorizer { return authorizer },
		}),
		auditlogd.Domain(auditlogd.Deps{
			Store: func() *store.Store[authmodel.AuditRecord] { return repos.Audit },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),
		orgd.Domain(orgd.Deps{
			OrgUnitStore:  func() *store.Store[authmodel.OrgUnit] { return repos.OrgUnit },
			PositionStore: func() *store.Store[authmodel.Position] { return repos.Position },
			UserStore:     func() *store.Store[authmodel.User] { return repos.User },
			Authn:         func() authn.Authenticator { return authenticator },
			Authz:         func() authz.Authorizer { return authorizer },
		}),
		languaged.Domain(languaged.Deps{
			Store: func() *store.Store[authmodel.Language] { return repos.Language },
			Authn: func() authn.Authenticator { return authenticator },
			Authz: func() authz.Authorizer { return authorizer },
		}),

		// --- 重域：auth ---
		// 登录防护三件（限流/熔断/重试）构造器住 authd 域（
		// config.go）：域 Wire 自读 Settings 快照的 biz.login.* 段并自行构造，装配层
		// 不再出现业务阈值；flag 通道经 authd.FlagOptions 声明（下方 Bind）。其余
		// 独有依赖（TokenStore/验证码/平台判据/TTL）按基础设施纪律住装配层——
		// BeforeStart 抉择，域经 Deps getter 迟到取用（本函数作用域变量）。
		authd.Domain(authd.Deps{
			Signer:         func() authnjwt.Signer { return bootstrappkg.Signer },
			UserStore:      func() *store.Store[authmodel.User] { return repos.User },
			TenantStore:    func() *store.Store[authmodel.Tenant] { return repos.Tenant },
			ReloadPolicies: func() func() error { return bootstrappkg.ReloadPolicies },
			Authn:          func() authn.Authenticator { return authenticator },
			TokenStore:     func() token.Store { return tokenStore },
			CaptchaStore:   func() captcha.Store { return captchaStore },
			PlatformResolver: func() func(*authmodel.User) bool {
				return authmodel.IsPlatformUser
			},
			TokenTTL: func() (time.Duration, time.Duration) {
				if svrOpts.Auth.AccessTTLMinutes <= 0 {
					return 0, 0 // 禁用 TTL 抉择：SetTokenTTL 对零值忽略 = 默认 TTL
				}
				var refresh time.Duration
				if svrOpts.Auth.RefreshTTLHours > 0 {
					refresh = time.Duration(svrOpts.Auth.RefreshTTLHours) * time.Hour
				}
				return time.Duration(svrOpts.Auth.AccessTTLMinutes) * time.Minute, refresh
			},
			Authz: func() authz.Authorizer { return authorizer },
		}),

		// --- 机械域：mfa/identity/task/message/dashboard/
		// permgroup/plan/portal/cachemonitor——biz 构造在各域
		// Wire（无 proto 纯 gin 面，Services 不声明合法；cachemonitor 无仓储，
		// Redis 客户端经 getter 透传，nil=监控降级合法）。getter 形态同上：
		// store 读 repos，authn/authz 读 BeforeStart 构造的函数作用域变量。
		mfad.Domain(mfad.Deps{
			FactorStore: func() *store.Store[authmodel.UserMFAFactor] { return repos.MFAFactor },
			Challenges:  func() mfa.ChallengeStore { return mfaChallenges },
			Authn:       func() authn.Authenticator { return authenticator },
			Authz:       func() authz.Authorizer { return authorizer },
		}),
		identityd.Domain(identityd.Deps{
			CredentialStore:  func() *store.Store[authmodel.UserCredential] { return repos.Credential },
			LoginPolicyStore: func() *store.Store[authmodel.LoginPolicy] { return repos.LoginPolicy },
			Authn:            func() authn.Authenticator { return authenticator },
			Authz:            func() authz.Authorizer { return authorizer },
		}),
		taskd.Domain(taskd.Deps{
			TaskStore: func() *store.Store[authmodel.Task] { return repos.Task },
			Authn:     func() authn.Authenticator { return authenticator },
			Authz:     func() authz.Authorizer { return authorizer },
		}),
		messaged.Domain(messaged.Deps{
			MessageStore:  func() *store.Store[authmodel.InternalMessage] { return repos.Message },
			CategoryStore: func() *store.Store[authmodel.InternalMessageCategory] { return repos.MessageCategory },
			RecipientStore: func() *store.Store[authmodel.InternalMessageRecipient] {
				return repos.Recipient
			},
			UserStore: func() *store.Store[authmodel.User] { return repos.User },
			Authn:     func() authn.Authenticator { return authenticator },
			Authz:     func() authz.Authorizer { return authorizer },
		}),
		dashboardd.Domain(dashboardd.Deps{
			AuditStore: func() *store.Store[authmodel.AuditRecord] { return repos.Audit },
			RoleStore:  func() *store.Store[authmodel.Role] { return repos.Role },
			UserStore:  func() *store.Store[authmodel.User] { return repos.User },
			Authn:      func() authn.Authenticator { return authenticator },
			Authz:      func() authz.Authorizer { return authorizer },
		}),
		permgroupd.Domain(permgroupd.Deps{
			GroupStore:  func() *store.Store[authmodel.PermissionGroup] { return repos.PermGroup },
			EvalLogStore: func() *store.Store[authmodel.PolicyEvaluationLog] { return repos.PolicyEvalLog },
			Authn:       func() authn.Authenticator { return authenticator },
			Authz:       func() authz.Authorizer { return authorizer },
		}),
		pland.Domain(pland.Deps{
			PlanStore:   func() *store.Store[authmodel.Plan] { return repos.Plan },
			ModuleStore: func() *store.Store[authmodel.PlanModule] { return repos.PlanModule },
			QuotaStore:  func() *store.Store[authmodel.PlanQuota] { return repos.PlanQuota },
			Authn:       func() authn.Authenticator { return authenticator },
			Authz:       func() authz.Authorizer { return authorizer },
		}),
		portald.Domain(portald.Deps{
			UserStore:       func() *store.Store[authmodel.User] { return repos.User },
			RoleStore:       func() *store.Store[authmodel.Role] { return repos.Role },
			PermissionStore: func() *store.Store[authmodel.Permission] { return repos.Permission },
			MenuStore:       func() *store.Store[authmodel.Menu] { return repos.Menu },
			Authn:           func() authn.Authenticator { return authenticator },
			Authz:           func() authz.Authorizer { return authorizer },
		}),
		cachemonitord.Domain(cachemonitord.Deps{
			RDB:   func() goredis.UniversalClient { return bootstrappkg.RedisClient },
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
		// 业务配置 flag 通道。缺此注册时业务 flag 进不了装载 FlagSet，
		// 「flag > env > 文件」对业务配置项整条失效。
		appkit.Bind("", svrOpts),
		// 登录防护三件的 flag 通道（--biz.login.*）：键名/默认值归 authd 域
		// （业务阈值不出域），装配层仅声明绑定（PlainBinder 形态）。
		appkit.Bind("", &authd.FlagOptions{}),

		// --- 能力声明（代码提供）---
		appkit.WithHTTP(router),
		// 拦截器链改**运行期求值**——工厂在 Run 期 server 构造点调用（业务
		// BeforeStart 之后），可读上方运行期赋值的 authenticator/authorizer。
		// （业务侧不传 gRPC 登记回调——见上方「gRPC service 注册」注释；
		// gRPC 面的存在性与 service 登记均由域声明包的 Services 承载。）
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

		// --- 机制层（mech 子包）---
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
			// 依赖段来源）。契约归 Serve 内部，业务侧
			// 经 Settings() 解码重建（含 flag/env/远程全源终值，
			// 覆盖文件基线）。
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
			// nil 为 no-op，InitBridges 走自建/降级）。资源经 Provider 导出的
			// 类型化句柄取用（Key[T]，类型不符即 miss）。
			if db, ok := storegorm.SQL.Resource(app); ok {
				bootstrappkg.WireDatabase(db)
			}
			if c, ok := CacheRedis.Resource(app); ok {
				bootstrappkg.WireCache(c)
			}
			if st, ok := miniocontract.MinIO.Resource(app); ok {
				bootstrappkg.WireStorage(st)
			}
			// storage.type=s3 时装配 s3 后端（与 minio 互斥择一）。
			// WireStorage 按实际类型分派，两者共用 ObjectStorageBridge。
			if st, ok := s3contract.S3.Resource(app); ok {
				bootstrappkg.WireStorage(st)
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
			// **次序至关重要**：路由注册需要带吊销检查的认证器，而吊销检查
			// 依赖 TokenStore（Redis）。若先注册路由再构造 TokenStore，
			// token.NewRevocationChecker 会因 store==nil 退化为纯验签——**吊销静默
			// 失效**（安全回归，正是 lazy 当初要修的 bug）。故 TokenStore 必须在此
			// （路由注册前）就绪。
			// 赋给**函数作用域**变量（非 `:=` 新建）——authd 域的 Deps.TokenStore
			// getter 读同一实例。
			tokenStore = buildTokenStore()
			if tokenStore != nil {
				bootstrappkg.TokenStore = tokenStore
				// 验证码 / MFA 挑战存储（复用同一 Redis）：随 TokenStore 同点抉择，
				// 域 Wire 经 Deps getter 迟到取用。无 Redis（tokenStore nil）时保持
				// nil——authd 的验证码与 mfad 的挑战存储各自禁用态（MFA fail-closed、
				// 验证码 503）。
				captchaStore = captcha.NewRedisStore(bootstrappkg.RedisClient)
				mfaChallenges = mfa.NewRedisChallengeStore(bootstrappkg.RedisClient)
			}
			// 认证器：有 Redis 时带吊销检查（登出即时生效）；无 Redis 退化为纯验签。
			// 赋给**函数作用域**变量（非 `:=` 新建）——运行期 gRPC 选项工厂
			// 与 SSE 工厂读取同一实例。
			authenticator = token.NewRevocationChecker(bootstrappkg.Authenticator, tokenStore)
			// 策略评估日志写入面接线：bootstrappkg.Authorizer 最外层的
			// RecordingAuthorizer 经本钩子在**请求期**迟到取用 permgroup 域 biz。
			// 此刻域 Wire 尚未执行（permgroupd.Biz() 仍为 nil），故注入 getter 而非
			// 实例——判空即降级不记录（typed-nil 装进接口 != nil，会空指针解引用）。
			// 授权器是所有域与 gRPC/SSE/管理面共用的同一实例，故一处接线覆盖全部
			// 判定，不止 permgroup 路由；写入失败/panic 不改判定（装饰器 best-effort）。
			bootstrappkg.SetEvalRecorder(func() appauthz.EvaluationRecorder {
				if b := permgroupd.Biz(); b != nil {
					return b
				}
				return nil
			})
			authorizer = bootstrappkg.Authorizer
			// 共面挂载（api 审计中间件 + service info + openapi）。21 域的路由
			// 由各域声明包的 Routes 注册——本钩子（业务 BeforeStart）先于域装配
			// 钩子执行（注册序=执行序），gin 的 Use 只影响其后注册的路由，故
			// apiaudit 中间件覆盖全部域路由；共面/管理面在域路由之前挂载。
			// （路由注册入口为 apiserver.RegisterBaseFaces 与各域 domain.go；
			// 业务 BeforeStart 只挂共面与管理面。）
			apiserver.RegisterBaseFaces(router)
			// 管理面：运行期组件观测与热插拔（appRef 在 provider Build 期绑定）。
			registerAdminRoutes(router, appRef, authenticator, authorizer, componentFactories)
			// secret/dict 两域的 Cache-Aside 后端解析（域 Wire 经 Deps.Cache getter
			// 迟到读取；file 的存储补注住 filed 的域 Wire——SetObjectStorage
			// 对 nil 不覆盖，桥接未就绪保留构造期 minio 直连）：
			// 配置驱动 RedisCache（cache.redis 段，含 password/db）优先；未配置段
			// 回退 wire 的 env 兼容通道（BALD_ADMIN_REDIS_ADDR，仅地址——CI 覆盖
			// 手段；addr 为空即禁用态 nil，biz 内判 nil 直连 store）。env 通道构造
			// 保持**无条件**（Redis 不可达仍启动失败，
			// 不静默降级）。生效后端单一：配置驱动 RedisCache 优先于 env 通道——
			// 即下方赋值的 if 优先序（bootstrappkg.RedisCache 非 nil 即用之）。
			envCache, err := newRedisCache(provideRedisAddr())
			if err != nil {
				return fmt.Errorf("build biz cache: %w", err)
			}
			if bootstrappkg.RedisCache != nil {
				bizCache = bootstrappkg.RedisCache
			} else {
				bizCache = envCache
			}
			// （auth/MFA 的 biz 接线——登录防护三件、平台判据、TokenStore/验证码/
			// 挑战存储、令牌 TTL——住 authd/mfad 域的 Wire：域自读
			// 自持配置段，基础设施经 Deps getter 迟到取用上面抉择好的实例。）
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

// Execute 构建并执行根命令（serve 为默认行为），返回错误交由调用方（main 包）
// 决定退出码——CLI 机制单轨进 pkg/appkit/appcli（bald ≥ v0.27.0）：FParseErr
// 白名单、k8s 风格 flag 错误 UX、--version / version 子命令、kubectl 风格插件
// 转发（前缀 = 二进制名，即 bald-admin-<name>，与旧手写版同行为；差异只有一条：
// 插件未找到时不再往 stderr 多打一行提示，交 cobra 原生处理未知参数）。
//
// **flag 的注册不在 cobra 层**——root 不注册任何 flag。全部 flag 由 Serve 路径的
// loadConfig 自建 pflag.FlagSet 注册，两类来源合流：框架 flag（appkit 内化的
// `--server.http.*` / `--server.grpc.*` / `--log.*` 壳，外加 --config）与业务
// flag（appkit.Bind("", svrOpts) → ServerOptions.AddFlags 注册 `--biz.*` 系，
// login 段键名归 authd.FlagOptions）。二者经同一个装载 FlagSet 合流，共享
// 「flag > env > 文件 > 远程」优先级。所以 cobra 只放行未知 flag——它识别子命令、
// 放行参数，不参与 flag 定义。
func Execute() error {
	return appcli.Main("bald-admin", "bald-admin 服务（bald 重构范例）", serveRunE,
		appcli.WithPlugins(),
		// doctor：与 serve 同一份装配 Options——报告必须与真实启动同源。
		appcli.WithDoctor(func(ctx context.Context) error {
			return appkit.Doctor(ctx, appOptions(options.NewServerOptions())...)
		}))
}
