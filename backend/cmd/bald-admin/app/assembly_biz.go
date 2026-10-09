// assembly_biz.go —— 业务对象装配（**手写**，非生成物）。
//
// 为什么手写而非用 DI 框架（wire/dig 等）：
//   - **本范例的定位是验证 bald 框架能力**。bald 的立场是「核心不内置 DI 框架，业务层自选」
//     （见 bald 核心原则 P1）——范例就该用**普通 Go 函数显式装配**，把「bald 的装配原语够用」
//     这件事演示出来，而不是引入一个框架再把依赖图藏进生成物里。
//   - **编译期保护已等价具备**：下面每个 `xxx.New(...)` 都是普通 Go 调用，biz 的 `New` 签名一变
//     此处立即编译失败——DI 框架宣称的「编译期依赖图校验」在此已被编译器本身提供。
//   - **依赖注入语义与 wire 不匹配**：仓储（33 个 `*store.Store[T]`，T 各不相同）经
//     `InitBridges` 的返回值 `Repositories` **参数传入**；`Signer`/`MinioStorage`/`FileBucket`/
//     `RedisClient` 是**运行期赋值**的包级变量。wire 按类型匹配，前者需 33 个命名类型包装、
//     后者只能退化成「provider 读全局」——与「仓储显式传入、不读包级」的方向相悖。
//
// 维护约定：**本文件与 `internal/apiserver/biz/v1/*` 的 `New` 签名保持同步**。
// 新增业务域时，在 `InitializeBiz` 内加一行 `xxxBiz := xxx.New(...)` 并补进 `BizSet` 字面量；
// 签名不匹配会由编译器指出，无需额外工具。
package app

import (
	"os"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	cmbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/cachemonitor"
	dashbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dashboard"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	identitybiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/identity"
	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
	mfabiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/mfa"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	planbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/plan"
	portalbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/portal"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	"github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald/cache"
	authnjwt "github.com/kalandramo/bald/contrib/authn-jwt"
)

// InitializeBiz 显式拼装 cache 与全部业务对象，产出 BizSet。
//
// 调用时机（关键约束）：必须在 `InitBridges` **之后**执行——仓储经其返回值 `repos` 传入，
// 届时已是已就绪的真实实例。调用点见 `assembly.go` 的 Run 期装配钩子；该时序由
// `assembly_e2e_test.go` 回归守护（若被挪回构造期，测试会红）。
//
// Wave 3：各 biz 的仓储改为**构造期注入**（此前 biz 在请求期读 bootstrap 包级变量）。
// Wave 5：仓储经参数传入（InitBridges 的返回值 Repositories），bootstrap 不再有包级 store 变量。
func InitializeBiz(repos *bootstrap.Repositories) (*apiserver.BizSet, error) {
	mainSigner := provideSigner()
	biz := auth.New(mainSigner, repos.User, repos.Tenant, bootstrap.ReloadPolicies)
	mainRedisAddr := provideRedisAddr()
	cache, err := newRedisCache(mainRedisAddr)
	if err != nil {
		return nil, err
	}
	secretBiz := secret.New(repos.Secret, cache)
	tenantBiz := tenant.New(repos.Tenant)
	userBiz := user.New(repos.User)
	menuBiz := menu.New(repos.Menu)
	permissionBiz := permission.New(repos.Permission, repos.RolePolicy)
	dictBiz := dict.New(repos.DictType, repos.DictEntry, cache)
	// T5：文件 biz——元数据仓储 Wave 3 起构造期注入（InitBridges 已赋值）。
	// MinIO 桥接与 file.bucket 兜底桶名仍为运行期：storage.minio/s3 段经契约
	// provider 在 FromBootstrap 阶段 B 构造，构造期可能为 nil，main.go 在
	// InitBridges 之后经 SetStorage/SetObjectStorage 补注；未配置时保持 nil，
	// biz 内判 nil 返回明确错误。
	fileBiz := file.New(repos.File, bootstrap.MinioStorage, bootstrap.FileBucket)
	// T6：审计查询 biz（只读，数据由写路径审计落库）。
	auditLogBiz := auditlog.New(repos.Audit)
	// Wave 1.5：MFA biz——因子仓储构造期注入；挑战存储（Redis）仍在 BeforeStart
	// 经 SetChallenges 补注（与 file.SetStorage / auth.SetCaptchaStore 同款时序约定）。
	mfaBiz := mfabiz.New(repos.MFAFactor, nil)
	// Wave 1.6：identity 扩展域（credential + login_policy）。
	identityBiz := identitybiz.New(repos.Credential, repos.LoginPolicy)
	// Wave 1.7：组织架构（org_unit 树 + position）。
	orgBiz := orgbiz.New(repos.OrgUnit, repos.Position, repos.User)
	// Wave 2.3：任务调度（scheduler 在 BeforeStart 后经 SetScheduler 注入）。
	taskBiz := taskbiz.New(repos.Task)
	// Wave 2.5：站内消息（三表）。
	messageBiz := msgbiz.New(repos.Message, repos.MessageCategory,
		repos.Recipient, repos.User)
	// Wave 3.4：首页分析（只读聚合）。
	dashboardBiz := dashbiz.New(repos.Audit, repos.Role, repos.User)
	// Wave 4.1：权限组 + 策略评估日志。
	permGroupBiz := pgbiz.New(repos.PermGroup, repos.PolicyEvalLog)
	// Wave 4.2：套餐三件套。
	planBiz := planbiz.New(repos.Plan, repos.PlanModule, repos.PlanQuota)
	languageBiz := langbiz.New(repos.Language)
	portalBiz := portalbiz.New(repos.User, repos.Role,
		repos.Permission, repos.Menu)
	cacheMonitorBiz := cmbiz.New(bootstrap.RedisClient)
	// T10：BizSet 收敛到 apiserver 包（bizset.go）；cache 留在装配局部
	// （仅 secret/dict 消费，server 层不知缓存实现）。
	bizSet := &apiserver.BizSet{
		Auth:         biz,
		Secret:       secretBiz,
		Tenant:       tenantBiz,
		User:         userBiz,
		Menu:         menuBiz,
		Permission:   permissionBiz,
		Dict:         dictBiz,
		File:         fileBiz,
		AuditLog:     auditLogBiz,
		MFA:          mfaBiz,
		Identity:     identityBiz,
		Org:          orgBiz,
		Task:         taskBiz,
		Message:      messageBiz,
		Dashboard:    dashboardBiz,
		PermGroup:    permGroupBiz,
		Plan:         planBiz,
		Language:     languageBiz,
		Portal:       portalBiz,
		CacheMonitor: cacheMonitorBiz,
	}
	return bizSet, nil
}

// redisAddr 是命名类型别名，区分 string 依赖（避免同名同型参数传错）。
type redisAddr string

// provideRedisAddr 从 env 提供 Redis 地址（空=禁用缓存，直连 store）。
// T0 起完整 Redis 参数（addr/password/db）经 bootstrap.Configure 由配置段注入，
// 此处仅保留 secret 缓存的 env 兼容通道。
func provideRedisAddr() redisAddr { return redisAddr(os.Getenv("BALD_ADMIN_REDIS_ADDR")) }

// provideSigner 从 bootstrap 取 RSA 私钥签发器（auth biz 依赖 Signer 接口）。
//
// Wave 4.2：InitializeBiz 已移到 InitBridges **之后**（Wave 4.1），故此处直接取
// 包级 Signer 即为已就绪的真实实例——原 LazySigner（请求期解析）已无必要，随之删除。
func provideSigner() authnjwt.Signer { return bootstrap.Signer }

// newRedisCache 适配 redisAddr→bootstrap.BuildRedisCache（D1：cache/redis 适配器；
// 保留错误，Redis 不可达即启动失败）。addr 为空返回 nil（禁用态）。
func newRedisCache(addr redisAddr) (cache.Cache, error) {
	return bootstrap.BuildRedisCache(string(addr), "", 0)
}
