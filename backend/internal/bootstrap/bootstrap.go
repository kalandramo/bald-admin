// Package bootstrap 负责桥接 bald 核心能力到本应用（M1 认证 + M2 数据化）。
//
// InitBridges 构造并保存：
//   - Authenticator：来自 bald-authn-jwt（HMAC 对称，MVP）；
//   - Authorizer：RBAC，角色→权限为静态策略，subject→角色从 store(User) 加载；
//   - DB / UserStore / RoleStore：bald-store-gorm 接入（M2，SQLite 内存库）。
//
// server 层装配中间件/gRPC service 时引用这些包级变量。
package bootstrap

import (
	"context"
	"fmt"
	"os"
	"sync"
	"strconv"
	"strings"
	"time"

	authnjwt "github.com/kalandramo/bald/contrib/authn-jwt"
	baldgorm "github.com/kalandramo/bald/contrib/store-gorm"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/kalandramo/bald/cache"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	miniooss "github.com/kalandramo/bald/oss/minio"
	s3oss "github.com/kalandramo/bald/oss/s3"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file/filestore"
	appauthz "github.com/kalandramo/bald-admin/internal/security/authz"
	"github.com/kalandramo/bald-admin/internal/security/token"
	casbinauthz "github.com/kalandramo/bald-admin/internal/security/casbin"
	"github.com/kalandramo/bald-admin/internal/security/audit/sqlaudit"
)

// Authenticator 登录令牌校验器（来自 bald-authn-jwt，公钥验签实例）。
// 注入 gin/grpc 认证拦截器，仅持 RSA 公钥——无法伪造 token。
var Authenticator authn.Authenticator

// Signer 令牌签发器（来自 bald-authn-jwt，私钥签发实例）。
// 注入 auth biz 的 Login，独占 RSA 私钥；与 Authenticator 分离，实现签发/验签权解耦。
var Signer authnjwt.Signer

// Authorizer 授权器（M6.1 起由 casbin 桥接 authz.Authorizer 实现）。
var Authorizer authz.Authorizer

// lazyAuthn / lazyAuthz 把上面的包级桥接变量适配为接口——路由/服务在 main 装配期
// 注册（此时 Authenticator/Authorizer 尚为 nil，InitBridges 在 appkit.BeforeStart
// 才赋值），而认证/授权中间件在请求期才真正调用。请求期经本适配器读取最新值。
//
// 背景（M10.1 修复的真实 bug）：此前 RegisterRoutes 直接传 bootstrappkg.Authenticator
// （构造期 nil），gin AuthnMiddleware 对 nil 退化为空操作——main 进程的 HTTP 认证
// 实际一直未生效（e2e 未经过 main 装配路径故未暴露）。lazy 适配器根治该时序错位。
type lazyAuthn struct{}

func (lazyAuthn) Authenticate(ctx context.Context) (*authn.AuthClaims, error) {
	return Authenticator.Authenticate(ctx)
}

func (lazyAuthn) AuthenticateToken(token string) (*authn.AuthClaims, error) {
	return Authenticator.AuthenticateToken(token)
}

type lazyAuthz struct{}

func (lazyAuthz) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	return Authorizer.Authorize(ctx, subject, object, action)
}

// LazyAuthenticator 返回延迟解析的认证器（请求期读 bootstrappkg.Authenticator 最新值）。
func LazyAuthenticator() authn.Authenticator { return lazyAuthn{} }

// LazyAuthorizer 返回延迟解析的授权器（请求期读 bootstrappkg.Authorizer 最新值）。
func LazyAuthorizer() authz.Authorizer { return lazyAuthz{} }

// lazySigner 把包级 Signer 桥接变量适配为 authnjwt.Signer。wire 的 provideSigner
// 在 main 构造期求值（InitializeBiz 先于 BeforeStart 的 InitBridges），直接传包级
// 变量会把 nil 快照固化进 auth Biz——login 签发即 nil panic（与 M10.1 修复的
// Authenticator 构造期 nil 同款时序错位，e2e 不走 main 装配路径故未暴露）。
// 本适配器在请求期读取最新值（与 lazyAuthn/lazyAuthz 同构）。
type lazySigner struct{}

func (lazySigner) IssueToken(claims authn.AuthClaims, ttl time.Duration) (string, error) {
	return Signer.IssueToken(claims, ttl)
}

// LazySigner 返回延迟解析的签发器（请求期读 bootstrappkg.Signer 最新值）。
func LazySigner() authnjwt.Signer { return lazySigner{} }

// TokenStore 是令牌服务端状态存储（Wave 1d：吊销名单 + 刷新令牌）。
// 由 BeforeStart 装配（复用 RedisClient）；nil = 无 Redis，降级语义见各调用点。
var TokenStore token.Store

// policyReloader 是 casbin 策略热重载钩子（Wave 1d-3）。
//
// 背景（框架能力缺口）：authz.Authorizer 接口只有 Authorize（pkg/authz/authz.go:15），
// contrib/authz-casbin 也无热重载入口——运行期新增用户/改角色后，权限必须重启
// 进程才生效（端到端实测：新注册用户 whoami 403）。应用层用
// security/authz.ReloadableAuthorizer 装饰器绕行。
//
// 为什么经包级变量而非直接依赖：authbiz 已 import bootstrap，若再让 bootstrap
// 依赖 authbiz 会成环；而注册流程（在 authbiz）需要触发重载。包级函数钩子是
// 最小耦合的桥接（与 Signer/TokenStore 同款的包级桥接模式）。
var policyReloader func() error

// SetPolicyReloader 由 main 装配时注入策略重载钩子（重建 casbin 策略快照）。
func SetPolicyReloader(fn func() error) { policyReloader = fn }

// ReloadPolicies 触发策略热重载（用户/角色变更后调用）。
// 未注入钩子时为无操作（返回 nil）——保持既有测试零改动。
func ReloadPolicies() error {
	if policyReloader == nil {
		return nil
	}
	return policyReloader()
}

// BuildAuthorizer 从 DB 重建 casbin 授权器（策略真源 = RolePolicy 表 + User.Roles）。
// 供 main 装配 ReloadableAuthorizer 的 build 函数使用（InitBridges 内的首次构造
// 也走同一逻辑，保证首次与重载语义一致）。
func BuildAuthorizer(ctx context.Context) (authz.Authorizer, error) {
	csv, err := loadPolicyCSV(ctx)
	if err != nil {
		return nil, err
	}
	return casbinauthz.New(csv)
}

// lazyAuthnWithRevocation 把「验签 + 吊销检查」适配为请求期解析的认证器。
//
// 为什么需要它（Wave 1d 实测的真实时序 bug）：
// RegisterRoutes 在**装配期**（main 早期）执行，而 RedisClient/TokenStore 要
// BeforeStart 才就绪——直接传 token.NewRevocationChecker(LazyAuthenticator(), ts)
// 会把 nil 快照固化，导致**吊销检查在生产路径静默失效**（e2e 走的是显式注入
// 路径，故未暴露；端到端 HTTP 验证抓到：登出后 whoami 仍返回 200）。
// 本适配器在**请求期**读取 TokenStore 最新值——与 lazyAuthn/lazySigner 同构。
type lazyAuthnWithRevocation struct{}

func (lazyAuthnWithRevocation) Authenticate(ctx context.Context) (*authn.AuthClaims, error) {
	return token.NewRevocationChecker(Authenticator, TokenStore).Authenticate(ctx)
}

func (lazyAuthnWithRevocation) AuthenticateToken(tokenStr string) (*authn.AuthClaims, error) {
	return token.NewRevocationChecker(Authenticator, TokenStore).AuthenticateToken(tokenStr)
}

// LazyAuthenticatorWithRevocation 返回「验签 + 吊销检查」的延迟解析认证器。
// 注入 gin/grpc 认证中间件，使 Logout 拉黑的 token 在**生产装配路径**也被拒绝。
func LazyAuthenticatorWithRevocation() authn.Authenticator { return lazyAuthnWithRevocation{} }

// DB 是应用主库（M2 起为 SQLite 内存库，T0 起默认经配置 database.sql 切外部 PostgreSQL）。
var DB *gorm.DB

// RedisClient 是底层 go-redis 客户端（可空：nil 表示无 Redis 环境）。
// 供审计流等「需要原生 Redis 命令」的消费方复用同一真实连接——D1 迁移后
// cache/redis 适配器不暴露底层 client（其 Close 亦不关闭 client），故由
// bootstrap 自行持有并管理生命周期。
var RedisClient goredis.UniversalClient

// RedisCache 是可选 Redis 缓存后端（M6.2 Cache-Aside；D1 由
// contrib/cache-redis 迁移到 cache/redis 适配器）。nil 表示无 Redis 环境，
// 调用方（缓存直连 store / 审计流降级）应降级。
var RedisCache cache.Cache

// MinioStorage 是可选 MinIO 对象存储后端（T0 起由 storage.minio 配置段构造，
// T5 文件模块消费）。SDK() 为 nil 表示未配置或构造失败，调用方应降级。
var MinioStorage *miniooss.Storage

// S3Storage 是可选 S3 兼容对象存储后端（Wave 5.4，storage.s3 配置段）。
// 与 MinioStorage 并列：二者按配置择一装配。
var S3Storage *s3oss.Storage

// ObjectStorageBridge 是文件模块实际消费的对象存储抽象（Wave 5.4）。
//
// 由 WireStorage 按注入的后端类型填充（minio → MinioAdapter；s3 → S3Adapter），
// 吸收两者**签名差异**（minio 逐调用传 bucket，s3 构造期固定 bucket）。
// nil 表示未配置对象存储，file biz 判 nil 降级。
var ObjectStorageBridge filestore.ObjectStorage

// FileBucket 是文件模块使用的对象存储桶（业务自持配置段 file.bucket；
// bconf storage.minio 契约无 bucket 字段，见移植计划 §5）。
var FileBucket string

// depsBootstrap 是 Configure 注入的框架契约配置（T0 真实依赖段）。
// BeforeStart 完成配置 Unmarshal 后调用 Configure，InitBridges 消费；
// 未调用时保持既有行为（env 变量 + SQLite 内存库），既有测试零改动。
var depsBootstrap *bootstrapv1.BootstrapConfig

// Configure 注入真实依赖配置：框架契约段（database.sql / cache.redis / storage.minio /
// registry.nacos，经 baldconfig.Unmarshal 填充）与业务自持段 fileBucket（file.bucket）。
// 幂等覆盖，须在 InitBridges 之前调用。
func Configure(cfg *bootstrapv1.BootstrapConfig, fileBucket string) {
	depsBootstrap = cfg
	FileBucket = fileBucket
}

// UserStore / RoleStore / SecretStore / TenantStore 是 bald-store-gorm 接入的泛型仓储。
// T3 新增 MenuStore（菜单树）/ PermissionStore（权限点注册表）/ RolePolicyStore
// （casbin p 行数据化，D3 策略装载真源）。
var UserStore *store.Store[authmodel.User]
var RoleStore *store.Store[authmodel.Role]
var SecretStore *store.Store[authmodel.Secret]
var TenantStore *store.Store[authmodel.Tenant]
var MenuStore *store.Store[authmodel.Menu]
var PermissionStore *store.Store[authmodel.Permission]
var RolePolicyStore *store.Store[authmodel.RolePolicy]
var DictTypeStore *store.Store[authmodel.DictType]
var DictEntryStore *store.Store[authmodel.DictEntry]

// LanguageStore 语言仓储（Wave 5.3；平台级数据，无租户维度）。
var LanguageStore *store.Store[authmodel.Language]

// FileStore 文件元数据仓储（T5；MinIO 对象经 MinioStorage 桥接，元数据落本库）。
var FileStore *store.Store[authmodel.File]

// AuditStore 审计日志仓储（T6 查询面；写路径经 security/audit.StoreAuditor 落库，
// 同表双通道：写走审计后端、读走本仓储）。
var AuditStore *store.Store[authmodel.AuditRecord]

// MFAFactorStore 用户 MFA 因子仓储（Wave 1.5）。
var MFAFactorStore *store.Store[authmodel.UserMFAFactor]

// CredentialStore 用户凭证仓储（Wave 1.6）。
var CredentialStore *store.Store[authmodel.UserCredential]

// LoginPolicyStore 登录策略仓储（Wave 1.6）。
var LoginPolicyStore *store.Store[authmodel.LoginPolicy]

// OrgUnitStore 组织单元仓储（Wave 1.7，树形）。
var OrgUnitStore *store.Store[authmodel.OrgUnit]

// PositionStore 职位仓储（Wave 1.7）。
var PositionStore *store.Store[authmodel.Position]

// TaskStore 任务定义仓储（Wave 2.3）。
var TaskStore *store.Store[authmodel.Task]

// PlanStore / PlanModuleStore / PlanQuotaStore 套餐三件套（Wave 4.2）。
var PlanStore *store.Store[authmodel.Plan]
var PlanModuleStore *store.Store[authmodel.PlanModule]
var PlanQuotaStore *store.Store[authmodel.PlanQuota]

// PermGroupStore / PolicyEvalLogStore 权限组与策略评估日志（Wave 4.1）。
var PermGroupStore *store.Store[authmodel.PermissionGroup]
var PolicyEvalLogStore *store.Store[authmodel.PolicyEvaluationLog]

// MessageStore / MessageCategoryStore / RecipientStore 站内消息三表（Wave 2.5）。
var MessageStore *store.Store[authmodel.InternalMessage]
var MessageCategoryStore *store.Store[authmodel.InternalMessageCategory]
var RecipientStore *store.Store[authmodel.InternalMessageRecipient]

// bridgesMu 串行化 InitBridges 的 check-then-act（UT8 修复：并发首调时
// 双 goroutine 同时通过 nil 判据、各自生成 RSA 密钥对、后写覆盖先写——
// 败者持有与胜者不同的 Signer 密钥，签发/验签闭环破坏）。用互斥而非
// sync.Once：Once.Do 失败也计数，与下方「失败路径重入重新生成密钥无害」
// 的语义冲突（失败须可重试）。
var bridgesMu sync.Mutex

// InitBridges 初始化认证/授权/存储桥接。
// 幂等：已完整初始化（DB 与 Authenticator 均非 nil）则直接返回，避免重复生成
// RSA 密钥对导致签发方与验签方密钥不一致（非对称下每次生成新密钥对，重复初始化
// 会破坏闭环）。判据必须是「装配完成」而非「步骤 1 已执行」：Authenticator 在
// openDB/seed 之前置位，若中途失败（如 DB 不可达）后重入，旧判据会直接返回 nil，
// 而 DB/stores 仍为 nil——下游 NPE；失败路径重入重新生成密钥对无害（彼时未对外
// 服务过任何 token）。
func InitBridges(ctx context.Context) error {
	bridgesMu.Lock()
	defer bridgesMu.Unlock()
	if DB != nil && Authenticator != nil {
		return nil
	}
	// 1) Authenticator（bald-authn-jwt，RSA 非对称）。
	// 演示用启动时生成 RSA-2048 密钥对：签发方持私钥、验证方只持公钥，实现签发/
	// 验签权解耦（核心收益：下游网关/微服务只需公钥即可验签，无需也不敢持有私钥）。
	// 生产应从 KMS / 固定 PEM 文件加载密钥（重启后旧 token 仍有效、私钥可进 HSM）。
	priv, err := authnjwt.GenerateRSA(2048)
	if err != nil {
		return fmt.Errorf("bootstrap: generate RSA key: %w", err)
	}
	Signer = authnjwt.NewAuthenticator(
		authnjwt.WithRSAKeys(priv, &priv.PublicKey),
		authnjwt.WithIssuer("go-bald-admin"),
		authnjwt.WithLeeway(0),
	)
	Authenticator = authnjwt.NewAuthenticator(
		authnjwt.WithRSAKeys(nil, &priv.PublicKey), // 仅公钥：验签方无法伪造
		authnjwt.WithIssuer("go-bald-admin"),
		authnjwt.WithLeeway(0),
	)

	// 2) Store（bald-store-gorm，M4 起 DSN 可配置）。
	// U1：DB/Redis/MinIO 优先消费注入实例（WireDatabase/WireCache/WireStorage，
	// FromBootstrap 阶段 B 经透传 provider 构造）；未注入才自建（既有测试与
	// 独立调用路径零改动）。默认 SQLite 内存库（M2），生产经 env
	// BALD_ADMIN_DB_DSN 切换到外部 PostgreSQL/MySQL。
	db := DB
	if db == nil {
		var err error
		if db, err = openDB(depsBootstrap.GetDatabase().GetSql()); err != nil {
			return err
		}
	}
	if err := db.AutoMigrate(&authmodel.User{}, &authmodel.Role{}, &authmodel.Secret{}, &authmodel.AuditRecord{}, &authmodel.Tenant{},
		&authmodel.Menu{}, &authmodel.Permission{}, &authmodel.RolePolicy{},
		&authmodel.DictType{}, &authmodel.DictEntry{}, &authmodel.File{},
		&authmodel.Language{},
		&authmodel.UserMFAFactor{}, &authmodel.UserCredential{}, &authmodel.LoginPolicy{},
		&authmodel.OrgUnit{}, &authmodel.Position{}, &authmodel.Task{},
		&authmodel.InternalMessage{}, &authmodel.InternalMessageCategory{},
		&authmodel.InternalMessageRecipient{},
		&authmodel.PermissionGroup{}, &authmodel.PolicyEvaluationLog{},
		&authmodel.Plan{}, &authmodel.PlanModule{}, &authmodel.PlanQuota{}); err != nil {
		return err
	}
	DB = db

	// Wave 5.2：注册 data_access 类审计的 gorm SQL 采集回调。
	// 源用 ent 的 dialect.Driver 包装采集 SQL；bald-admin 用 gorm，对应机制是
	// Callback（见 sqlaudit 包头注）。**必须在 AutoMigrate 之后**——否则迁移
	// 期间的 DDL 也会被采集（噪音），且审计表尚未建好。
	// 防重入：sqlaudit 在落库审计时打 ctx sinking 标记 + 表名双保险。
	sqlaudit.Register(db)

	// 可选 Redis 后端：供缓存/审计流复用同一真实连接。不可达仅 warn（审计流降级），
	// 不阻断启动（与 SQLite 内存库同构的"真实但可选"简化，符合 §0）。
	// U1 起：注入态优先（WireCache 已注入则跳过自建），否则 resolveRedis
	// 解析（env BALD_ADMIN_REDIS_ADDR 优先，其次配置段 cache.redis）。
	// D1：构造走 BuildRedisCache（cache/redis 适配器 + 探活），连接记入 RedisClient。
	if RedisCache == nil {
		redisAddr, redisPassword, redisDB := resolveRedis()
		rc, rerr := BuildRedisCache(redisAddr, redisPassword, redisDB)
		if rerr != nil {
			log.Warn(ctx, "redis init skipped, audit stream disabled", "error", rerr.Error())
		} else {
			RedisCache = rc
		}
	}

	// T0：MinIO 对象存储后端（storage.minio 配置段）。minio.New 仅本地构造不联网，
	// 失败时 SDK() 为 nil + 日志；真实可达性与建桶由 T5 文件模块 EnsureBucket 兜底，
	// 此处失败不阻断启动（与 Redis 同构的"真实但可选"）。U1 同款注入优先。
	if MinioStorage == nil {
		if mc := depsBootstrap.GetStorage().GetMinio(); mc != nil && mc.GetEndpoint() != "" {
			MinioStorage = miniooss.NewStorage(&miniooss.Config{
				Endpoint:  mc.GetEndpoint(),
				AccessKey: mc.GetAccessKey(),
				SecretKey: mc.GetSecretKey(),
				Token:     mc.GetToken(),
				UseSsl:    mc.GetUseSsl(),
			})
			if MinioStorage == nil || MinioStorage.SDK() == nil {
				MinioStorage = nil
				log.Warn(ctx, "minio init failed, file module degraded", "endpoint", mc.GetEndpoint())
			} else {
				log.Info(ctx, "minio storage constructed", "endpoint", mc.GetEndpoint())
			}
		}
	}
	UserStore = store.NewStore[authmodel.User](baldgorm.NewGormProvider(db, func(u *authmodel.User) string { return u.ID }))
	RoleStore = store.NewStore[authmodel.Role](baldgorm.NewGormProvider(db, func(r *authmodel.Role) string { return r.ID }))
	SecretStore = store.NewStore[authmodel.Secret](baldgorm.NewGormProvider(db, func(s *authmodel.Secret) string { return s.ID }))
	TenantStore = store.NewStore[authmodel.Tenant](baldgorm.NewGormProvider(db, func(t *authmodel.Tenant) string { return t.ID }))
	MenuStore = store.NewStore[authmodel.Menu](baldgorm.NewGormProvider(db, func(m *authmodel.Menu) string { return m.ID }))
	PermissionStore = store.NewStore[authmodel.Permission](baldgorm.NewGormProvider(db, func(p *authmodel.Permission) string { return p.ID }))
	RolePolicyStore = store.NewStore[authmodel.RolePolicy](baldgorm.NewGormProvider(db,
		func(p *authmodel.RolePolicy) string { return p.ID }))
	DictTypeStore = store.NewStore[authmodel.DictType](baldgorm.NewGormProvider(db, func(t *authmodel.DictType) string { return t.ID }))
	DictEntryStore = store.NewStore[authmodel.DictEntry](baldgorm.NewGormProvider(db, func(e *authmodel.DictEntry) string { return e.ID }))
	LanguageStore = store.NewStore[authmodel.Language](baldgorm.NewGormProvider(db, func(l *authmodel.Language) string { return l.ID }))
	FileStore = store.NewStore[authmodel.File](baldgorm.NewGormProvider(db, func(f *authmodel.File) string { return f.ID }))
	// T6：审计查询仓储（自增主键；审计表全量记录不走租户读隔离，ID 提取器照常）。
	AuditStore = store.NewStore[authmodel.AuditRecord](baldgorm.NewGormProvider(db, func(r *authmodel.AuditRecord) string {
		return strconv.FormatUint(uint64(r.ID), 10)
	}))
	// Wave 1.5：MFA 因子仓储（业务键主键，同 RolePolicy 范式）。
	MFAFactorStore = store.NewStore[authmodel.UserMFAFactor](baldgorm.NewGormProvider(db,
		func(f *authmodel.UserMFAFactor) string { return f.ID }))
	// Wave 1.6：凭证 + 登录策略仓储（业务键主键）。
	CredentialStore = store.NewStore[authmodel.UserCredential](baldgorm.NewGormProvider(db,
		func(c *authmodel.UserCredential) string { return c.ID }))
	LoginPolicyStore = store.NewStore[authmodel.LoginPolicy](baldgorm.NewGormProvider(db,
		func(p *authmodel.LoginPolicy) string { return p.ID }))
	// Wave 1.7：组织单元（树）+ 职位仓储。
	OrgUnitStore = store.NewStore[authmodel.OrgUnit](baldgorm.NewGormProvider(db,
		func(o *authmodel.OrgUnit) string { return o.ID }))
	PositionStore = store.NewStore[authmodel.Position](baldgorm.NewGormProvider(db,
		func(p *authmodel.Position) string { return p.ID }))
	// Wave 2.3：任务定义仓储。
	TaskStore = store.NewStore[authmodel.Task](baldgorm.NewGormProvider(db,
		func(t *authmodel.Task) string { return t.ID }))
	// Wave 2.5：站内消息三表。
	MessageStore = store.NewStore[authmodel.InternalMessage](baldgorm.NewGormProvider(db,
		func(m *authmodel.InternalMessage) string { return m.ID }))
	MessageCategoryStore = store.NewStore[authmodel.InternalMessageCategory](baldgorm.NewGormProvider(db,
		func(c *authmodel.InternalMessageCategory) string { return c.ID }))
	RecipientStore = store.NewStore[authmodel.InternalMessageRecipient](baldgorm.NewGormProvider(db,
		func(r *authmodel.InternalMessageRecipient) string { return r.ID }))
	// Wave 4.1：权限组 + 策略评估日志。
	PermGroupStore = store.NewStore[authmodel.PermissionGroup](baldgorm.NewGormProvider(db,
		func(g *authmodel.PermissionGroup) string { return g.ID }))
	PolicyEvalLogStore = store.NewStore[authmodel.PolicyEvaluationLog](baldgorm.NewGormProvider(db,
		func(l *authmodel.PolicyEvaluationLog) string { return l.ID }))
	// Wave 4.2：套餐三件套。
	PlanStore = store.NewStore[authmodel.Plan](baldgorm.NewGormProvider(db,
		func(p *authmodel.Plan) string { return p.ID }))
	PlanModuleStore = store.NewStore[authmodel.PlanModule](baldgorm.NewGormProvider(db,
		func(m *authmodel.PlanModule) string { return m.ID }))
	PlanQuotaStore = store.NewStore[authmodel.PlanQuota](baldgorm.NewGormProvider(db,
		func(q *authmodel.PlanQuota) string { return q.ID }))
	if err := seed(ctx); err != nil {
		return err
	}

	// 2.5) 多租户隔离（P8）：注册 tenant_id 维度，Store 查询自动注入等值过滤，
	// 业务 handler 无需手写，避免跨租户数据泄漏。DefaultTenantFunc 读取 authn
	// 认证后注入 contextx 的 TenantID。
	store.RegisterTenant("tenant_id", store.DefaultTenantFunc)

	// 3) Authorizer：M6.1 起由 casbin 桥接 authz.Authorizer 实现（P11 起实现晋升 contrib
	//    bald-authz-casbin，内嵌通用 RBAC 模型）。T3 起策略数据化装载（D3）：p 行读
	//    RolePolicy 表、g 行读 User.Roles——静态 rbac_policy.csv 已删除，策略单一真源
	//    收敛到 DB（改库重启即生效；无策略行时 casbin 默认拒绝，fail-closed）。
	policyCSV, err := loadPolicyCSV(ctx)
	if err != nil {
		return err
	}
	az, err := casbinauthz.New(policyCSV)
	if err != nil {
		return err
	}
	// Wave 1d-3：用可热重载装饰器包装——运行期新增用户/改角色后，
	// 业务调用 ReloadPolicies() 即可让新权限生效（否则须重启进程）。
	// 重建函数复用 BuildAuthorizer（与首次构造同一逻辑，语义一致）。
	reloadable := appauthz.NewReloadable(az, func() (authz.Authorizer, error) {
		return BuildAuthorizer(context.Background())
	})
	Authorizer = reloadable
	SetPolicyReloader(reloadable.Reload)

	log.Info(ctx, "bridges initialized",
		"authenticator", "bald-authn-jwt", "authorizer", "casbin", "store", "bald-store-gorm")
	return nil
}

// seed 写入 MVP 初始用户与角色（生产应走迁移脚本/初始化任务）。
func seed(ctx context.Context) error {
	// T2 租户种子：platform（平台租户，源 PlatformTenantID=0 等价物）、
	// t-default/t-other 与存量 users/secrets 的租户维度对齐。
	tenants := []*authmodel.Tenant{
		{ID: "platform", Name: "平台租户", Status: "ON", Remark: "平台管理上下文（源 PlatformTenantID=0 等价物）"},
		{ID: "t-default", Name: "默认租户", Status: "ON"},
		{ID: "t-other", Name: "第二租户", Status: "ON", Remark: "多租户隔离验证用"},
	}
	for _, t := range tenants {
		if err := TenantStore.Create(ctx, t); err != nil && err != store.ErrConflict {
			return err
		}
	}
	roles := []*authmodel.Role{
		{ID: "admin", Perms: "secret:get,secret:delete,auth:get,SecretService.GetSecret:call,SecretService.DeleteSecret:call,UserService.ListUsers:call,AuthService.WhoAmI:call"},
		{ID: "viewer", Perms: "secret:get,auth:get,SecretService.GetSecret:call,UserService.ListUsers:call"},
	}
	for _, r := range roles {
		if err := RoleStore.Create(ctx, r); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// 密码以 bcrypt 哈希写入（M3 起，不再明文存储）。
	adminHash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	aliceHash, err := bcrypt.GenerateFromPassword([]byte("alice123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	users := []*authmodel.User{
		{ID: "u-admin", Username: "admin", PasswordHash: string(adminHash), TenantID: "t-default", Roles: "admin"},
		{ID: "u-alice", Username: "alice", PasswordHash: string(aliceHash), TenantID: "t-default", Roles: "viewer"},
		// 第二租户用户：无 secret 权限，用于验证多租户隔离（越权跨租户检索被拦）。
		{ID: "u-bob", Username: "bob", PasswordHash: string(aliceHash), TenantID: "t-other", Roles: "viewer"},
	}
	for _, u := range users {
		if err := UserStore.Create(ctx, u); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// 受限资源：跨租户落 1 条 t-default + 1 条 t-other，验证 M6.3 真实 DAL 与多租户隔离。
	secrets := []*authmodel.Secret{
		{ID: "s-db-pwd", Name: "数据库口令", Content: "rds-t-default-9f3a2c", TenantID: "t-default"},
		{ID: "s-api-key", Name: "开放平台密钥", Content: "ak-t-default-77be10", TenantID: "t-default"},
		{ID: "s-other-pwd", Name: "他租户口令", Content: "rds-t-other-1d4e8f", TenantID: "t-other"},
	}
	for _, s := range secrets {
		if err := SecretStore.Create(ctx, s); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// T3 菜单种子（自 go-wind-admin DefaultMenus 形态精简：2 目录 + 6 页面 + 1 按钮，
	// 树形 ParentID 显式编死，Order 控制展示顺序——源 meta.order 语义）。
	menus := []*authmodel.Menu{
		{ID: "menu-dashboard", Type: "CATALOG", Name: "Dashboard", Path: "/dashboard", Title: "仪表盘", Icon: "lucide:layout-dashboard", Order: -1},
		{ID: "menu-dashboard-overview", ParentID: "menu-dashboard", Type: "MENU", Name: "Overview", Path: "/dashboard/overview", Title: "总览", Order: 1},
		{ID: "menu-system", Type: "CATALOG", Name: "System", Path: "/system", Title: "系统管理", Icon: "lucide:settings", Order: 2000},
		{ID: "menu-tenant", ParentID: "menu-system", Type: "MENU", Name: "Tenant", Path: "/tenant", Title: "租户管理", Order: 1},
		{ID: "menu-user", ParentID: "menu-system", Type: "MENU", Name: "User", Path: "/user", Title: "用户管理", Order: 2},
		{ID: "menu-menu", ParentID: "menu-system", Type: "MENU", Name: "Menu", Path: "/menu", Title: "菜单管理", Order: 3},
		{ID: "menu-permission", ParentID: "menu-system", Type: "MENU", Name: "Permission", Path: "/permission", Title: "权限管理", Order: 4},
		{ID: "menu-secret", ParentID: "menu-system", Type: "MENU", Name: "Secret", Path: "/secret", Title: "机密管理", Order: 5},
		{ID: "menu-dict", ParentID: "menu-system", Type: "MENU", Name: "Dict", Path: "/dict", Title: "字典管理", Order: 6},
		{ID: "menu-file", ParentID: "menu-system", Type: "MENU", Name: "File", Path: "/file", Title: "文件管理", Order: 7},
		{ID: "menu-audit", ParentID: "menu-system", Type: "MENU", Name: "Audit", Path: "/audit", Title: "审计日志", Order: 8},
		{ID: "menu-secret-delete", ParentID: "menu-secret", Type: "BUTTON", Name: "DeleteSecret", Path: "secret:delete", Title: "删除机密", Order: 1},
	}
	for _, m := range menus {
		if err := MenuStore.Create(ctx, m); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// T3 权限点种子（注册表 + 菜单可见性关联；未注册的权限码仍可被 Role.Perms
	// 引用——本表是元数据，非授权判定依据）。
	perms := []*authmodel.Permission{
		{ID: "auth:get", Name: "访问认证接口"},
		{ID: "secret:list", Name: "列出机密", MenuIDs: "menu-secret"},
		{ID: "secret:delete", Name: "删除机密", MenuIDs: "menu-secret-delete"},
		{ID: "tenant:list", Name: "列出租户", MenuIDs: "menu-tenant"},
		{ID: "user:list", Name: "列出用户", MenuIDs: "menu-user"},
		{ID: "menu:list", Name: "列出菜单", MenuIDs: "menu-menu"},
		{ID: "permission:list", Name: "列出权限", MenuIDs: "menu-permission"},
		{ID: "dict_type:list", Name: "列出字典类型", MenuIDs: "menu-dict"},
		{ID: "dict_entry:list", Name: "列出字典项", MenuIDs: "menu-dict"},
		{ID: "file:list", Name: "列出文件", MenuIDs: "menu-file"},
		{ID: "audit:list", Name: "查询审计日志", MenuIDs: "menu-audit"},
	}
	for _, p := range perms {
		if err := PermissionStore.Create(ctx, p); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// T4 字典种子（自 go-wind-admin demo 数据形态精简：3 组类型 + 8 条条目）。
	// 字典是租户级业务数据（源 mixin TenantID），种子归属 t-default——租户字段
	// 由实体显式携带（启动 ctx 无租户 claims，injectWriteTenant 跳过注入，与
	// Secret 种子同构）。
	dictTypes := []*authmodel.DictType{
		{ID: "gender", TenantID: "t-default", TypeName: "性别", SortOrder: 1},
		{ID: "status", TenantID: "t-default", TypeName: "状态", SortOrder: 2},
		{ID: "yes_no", TenantID: "t-default", TypeName: "是否", SortOrder: 3},
	}
	for _, t := range dictTypes {
		if err := DictTypeStore.Create(ctx, t); err != nil && err != store.ErrConflict {
			return err
		}
	}
	num1, num2, num3 := int32(1), int32(2), int32(3)
	dictEntries := []*authmodel.DictEntry{
		{ID: "gender:male", TenantID: "t-default", TypeCode: "gender", Value: "male", Label: "男", SortOrder: 1},
		{ID: "gender:female", TenantID: "t-default", TypeCode: "gender", Value: "female", Label: "女", SortOrder: 2},
		{ID: "gender:unknown", TenantID: "t-default", TypeCode: "gender", Value: "unknown", Label: "未知", Numeric: &num3, SortOrder: 3},
		{ID: "status:on", TenantID: "t-default", TypeCode: "status", Value: "on", Label: "启用", Numeric: &num1, SortOrder: 1},
		{ID: "status:off", TenantID: "t-default", TypeCode: "status", Value: "off", Label: "禁用", Numeric: &num2, SortOrder: 2},
		{ID: "status:frozen", TenantID: "t-default", TypeCode: "status", Value: "frozen", Label: "冻结", Numeric: &num3, SortOrder: 3},
		{ID: "yes_no:y", TenantID: "t-default", TypeCode: "yes_no", Value: "y", Label: "是", Numeric: &num1, SortOrder: 1},
		{ID: "yes_no:n", TenantID: "t-default", TypeCode: "yes_no", Value: "n", Label: "否", Numeric: &num2, SortOrder: 2},
	}
	for _, e := range dictEntries {
		if err := DictEntryStore.Create(ctx, e); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// Wave 5.3 语言种子：对齐源 constants.DefaultLanguages 的 7 条
	// （zh-CN 为默认；sort_order 源值 0/1/100——zh-CN 最小在前）。
	languages := []*authmodel.Language{
		{ID: "zh-CN", LanguageName: "中文（简体）", NativeName: "简体中文", IsDefault: true, IsEnabled: true, SortOrder: 0},
		{ID: "en-US", LanguageName: "英语", NativeName: "English", IsEnabled: true, SortOrder: 1},
		{ID: "zh-TW", LanguageName: "中文（繁体）", NativeName: "繁體中文", IsEnabled: true, SortOrder: 100},
		{ID: "ja-JP", LanguageName: "日语", NativeName: "日本語", IsEnabled: true, SortOrder: 100},
		{ID: "ko-KR", LanguageName: "韩语", NativeName: "한국어", IsEnabled: true, SortOrder: 100},
		{ID: "es-ES", LanguageName: "西班牙语", NativeName: "Español", IsEnabled: true, SortOrder: 100},
		{ID: "fr-FR", LanguageName: "法语", NativeName: "Français", IsEnabled: true, SortOrder: 100},
	}
	for _, l := range languages {
		if err := LanguageStore.Create(ctx, l); err != nil && err != store.ErrConflict {
			return err
		}
	}
	// T3 角色策略种子（casbin p 行数据化，D3）：内容与被删除的静态 rbac_policy.csv
	// 等价（存量授权行为零回归）+ menu/permission 管理域（仅 admin 写，viewer 只读）。
	// RolePolicy 自增主键无业务唯一键，防重复种子用「非空即跳过」。
	return seedPolicies(ctx)
}

// seedPolicies 种子化 casbin p 行（角色策略）。等价旧静态 rbac_policy.csv 的
// 全部授权语义；g 行（subject→角色）不落表，装载时从 User.Roles 生成。
// 主键 role:object:action 冲突即重复策略，防重与其他种子同构（ErrConflict 容忍）。
func seedPolicies(ctx context.Context) error {
	policies := []authmodel.RolePolicy{
		// admin：机密/认证/管理面（M6.1 起存量）。
		{Role: "admin", Object: "secret", Action: "get"},
		{Role: "admin", Object: "secret", Action: "delete"},
		{Role: "admin", Object: "secret", Action: "list"},
		{Role: "admin", Object: "auth", Action: "get"},
		// Wave 1d-4：token 管理（GetAccessTokens/BlockToken/UnblockToken/RevokeTokenById）
		// 是**写操作**（POST）——授权归一化把 POST 映射为 "post"（DefaultHTTPAction）。
		// 此前 admin 只有 {auth, get}，故 token 管理路由全部 403（实测）。
		{Role: "admin", Object: "auth", Action: "post"},
		// Wave 1.5：MFA 域。
		// **admin 与 viewer 都需 {mfa, get/post}**——MFA 是用户对**自己账号**的
		// 安全设置，任何登录用户都应能绑定/解绑自己的因子（源的 MFA 管理面
		// 从 auth.FromContext 取 operator，强制只能操作本人）。
		// 越权面（操作**他人**因子）由 biz 层的归属校验兜底，不靠权限点区分。
		{Role: "admin", Object: "mfa", Action: "get"},
		{Role: "admin", Object: "mfa", Action: "post"},
		{Role: "viewer", Object: "mfa", Action: "get"},
		{Role: "viewer", Object: "mfa", Action: "post"},
		// Wave 1.6：凭证与登录策略——**安全敏感，仅 admin**。
		// viewer 不得读写凭证/策略（凭证明文操作、策略配错会锁全员）。
		{Role: "admin", Object: "credentials", Action: "get"},
		{Role: "admin", Object: "credentials", Action: "post"},
		{Role: "admin", Object: "credentials", Action: "delete"},
		{Role: "admin", Object: "credentials", Action: "put"},
		{Role: "admin", Object: "login-policies", Action: "get"},
		{Role: "admin", Object: "login-policies", Action: "post"},
		{Role: "admin", Object: "login-policies", Action: "delete"},
		{Role: "admin", Object: "login-policies", Action: "put"},
		{Role: "admin", Object: "profile", Action: "get"},
		{Role: "admin", Object: "profile", Action: "post"},
		// viewer 只能操作自己的凭证（改密码），不能读他人。
		{Role: "viewer", Object: "credentials", Action: "post"},
		// Wave 1.7：组织架构（org_unit / position）。
		// admin 全权；viewer 只读（组织架构是全员可见的公共组织信息）。
		{Role: "admin", Object: "org-units", Action: "get"},
		{Role: "admin", Object: "org-units", Action: "post"},
		{Role: "admin", Object: "org-units", Action: "put"},
		{Role: "admin", Object: "org-units", Action: "delete"},
		{Role: "admin", Object: "positions", Action: "get"},
		{Role: "admin", Object: "positions", Action: "post"},
		{Role: "admin", Object: "positions", Action: "put"},
		{Role: "admin", Object: "positions", Action: "delete"},
		{Role: "viewer", Object: "org-units", Action: "get"},
		{Role: "viewer", Object: "positions", Action: "get"},
		// Wave 2.3：任务调度——**仅 admin**（启停任务影响后台作业，属运维面）。
		{Role: "admin", Object: "tasks", Action: "get"},
		{Role: "admin", Object: "tasks", Action: "post"},
		{Role: "admin", Object: "tasks", Action: "put"},
		{Role: "admin", Object: "tasks", Action: "delete"},
		// Wave 2.5：站内消息。
		// 管理面（消息/分类 CRUD、发送、撤销）→ 仅 admin；
		// **收件箱 → 任何登录用户**（操作自己的收件箱，源同此语义）。
		{Role: "admin", Object: "messages", Action: "get"},
		{Role: "admin", Object: "messages", Action: "post"},
		{Role: "admin", Object: "messages", Action: "put"},
		{Role: "admin", Object: "messages", Action: "delete"},
		{Role: "admin", Object: "message-categories", Action: "get"},
		{Role: "admin", Object: "message-categories", Action: "post"},
		{Role: "admin", Object: "message-categories", Action: "put"},
		{Role: "admin", Object: "message-categories", Action: "delete"},
		{Role: "viewer", Object: "inbox", Action: "get"},
		{Role: "viewer", Object: "inbox", Action: "post"},
		{Role: "viewer", Object: "inbox", Action: "delete"},
		{Role: "admin", Object: "inbox", Action: "get"},
		{Role: "admin", Object: "inbox", Action: "post"},
		{Role: "admin", Object: "inbox", Action: "delete"},
		{Role: "admin", Object: "recipients", Action: "get"},
		{Role: "admin", Object: "recipients", Action: "post"},
		// Wave 3.4：dashboard 只读聚合（仅 admin——分析页是管理面功能）。
		{Role: "admin", Object: "dashboard", Action: "get"},
		// Wave 4.1：权限组 + 策略评估日志（仅 admin——权限配置是管理面功能）。
		{Role: "admin", Object: "permission-groups", Action: "get"},
		{Role: "admin", Object: "permission-groups", Action: "post"},
		{Role: "admin", Object: "permission-groups", Action: "put"},
		{Role: "admin", Object: "permission-groups", Action: "delete"},
		{Role: "admin", Object: "policy-evaluation-logs", Action: "get"},
		// Wave 4.2：套餐三件套（仅 admin）。
		{Role: "admin", Object: "plans", Action: "get"},
		{Role: "admin", Object: "plans", Action: "post"},
		{Role: "admin", Object: "plans", Action: "put"},
		{Role: "admin", Object: "plans", Action: "delete"},
		{Role: "admin", Object: "plan-modules", Action: "get"},
		{Role: "admin", Object: "plan-modules", Action: "post"},
		{Role: "admin", Object: "plan-modules", Action: "put"},
		{Role: "admin", Object: "plan-modules", Action: "delete"},
		{Role: "admin", Object: "plan-quotas", Action: "get"},
		{Role: "admin", Object: "plan-quotas", Action: "post"},
		{Role: "admin", Object: "plan-quotas", Action: "put"},
		{Role: "admin", Object: "plan-quotas", Action: "delete"},
		{Role: "admin", Object: "admin", Action: "get"},
		{Role: "admin", Object: "admin", Action: "post"},
		{Role: "admin", Object: "admin", Action: "delete"},
		// admin：租户/用户管理（T2）。
		{Role: "admin", Object: "tenant", Action: "get"},
		{Role: "admin", Object: "tenant", Action: "post"},
		{Role: "admin", Object: "tenant", Action: "put"},
		{Role: "admin", Object: "tenant", Action: "delete"},
		{Role: "admin", Object: "tenant", Action: "list"},
		{Role: "admin", Object: "tenant", Action: "write"},
		{Role: "admin", Object: "user", Action: "get"},
		{Role: "admin", Object: "user", Action: "post"},
		{Role: "admin", Object: "user", Action: "put"},
		{Role: "admin", Object: "user", Action: "delete"},
		{Role: "admin", Object: "user", Action: "list"},
		{Role: "admin", Object: "user", Action: "write"},
		// admin：菜单/权限管理（T3 新增管理域）。动作六元组（get/post/put/delete/
		// list/write）：REST 归一化为 HTTP 动词小写（DefaultHTTPAction），gRPC 归一化为
		// get/list/write（DefaultGRPCAction，Create/Update→write）——双协议同源全放行。
		{Role: "admin", Object: "menu", Action: "get"},
		{Role: "admin", Object: "menu", Action: "post"},
		{Role: "admin", Object: "menu", Action: "put"},
		{Role: "admin", Object: "menu", Action: "delete"},
		{Role: "admin", Object: "menu", Action: "list"},
		{Role: "admin", Object: "menu", Action: "write"},
		{Role: "admin", Object: "permission", Action: "get"},
		{Role: "admin", Object: "permission", Action: "post"},
		{Role: "admin", Object: "permission", Action: "put"},
		{Role: "admin", Object: "permission", Action: "delete"},
		{Role: "admin", Object: "permission", Action: "list"},
		{Role: "admin", Object: "permission", Action: "write"},
		// viewer：只读（存量）。
		{Role: "viewer", Object: "secret", Action: "get"},
		{Role: "viewer", Object: "secret", Action: "list"},
		{Role: "viewer", Object: "auth", Action: "get"},
		{Role: "viewer", Object: "user", Action: "get"},
		{Role: "viewer", Object: "user", Action: "list"},
		// viewer：菜单/权限只读（T3，前端渲染可见性；不持有写动作）。
		{Role: "viewer", Object: "menu", Action: "get"},
		{Role: "viewer", Object: "menu", Action: "list"},
		{Role: "viewer", Object: "permission", Action: "get"},
		{Role: "viewer", Object: "permission", Action: "list"},
		// admin：字典管理（T4）。dict_type/dict_entry 同款六元组（REST HTTP 动词
		// 小写 + gRPC get/list/write 双协议全放行）。
		{Role: "admin", Object: "dict_type", Action: "get"},
		{Role: "admin", Object: "dict_type", Action: "post"},
		{Role: "admin", Object: "dict_type", Action: "put"},
		{Role: "admin", Object: "dict_type", Action: "delete"},
		{Role: "admin", Object: "dict_type", Action: "list"},
		{Role: "admin", Object: "dict_type", Action: "write"},
		{Role: "admin", Object: "dict_entry", Action: "get"},
		{Role: "admin", Object: "dict_entry", Action: "post"},
		{Role: "admin", Object: "dict_entry", Action: "put"},
		{Role: "admin", Object: "dict_entry", Action: "delete"},
		{Role: "admin", Object: "dict_entry", Action: "list"},
		{Role: "admin", Object: "dict_entry", Action: "write"},
		// viewer：字典只读（T4，业务数据读取开放给登录用户）。
		{Role: "viewer", Object: "dict_type", Action: "get"},
		{Role: "viewer", Object: "dict_type", Action: "list"},
		{Role: "viewer", Object: "dict_entry", Action: "get"},
		{Role: "viewer", Object: "dict_entry", Action: "list"},
		// admin：语言管理（Wave 5.3）。object 归一化：REST /v1/language →
		// DefaultHTTPObject="language"；gRPC LanguageService/* →
		// DefaultGRPCObject="language"（去 Service 后缀）——双协议同源。
		{Role: "admin", Object: "language", Action: "get"},
		{Role: "admin", Object: "language", Action: "post"},
		{Role: "admin", Object: "language", Action: "put"},
		{Role: "admin", Object: "language", Action: "delete"},
		{Role: "admin", Object: "language", Action: "list"},
		{Role: "admin", Object: "language", Action: "write"},
		// viewer：语言只读（语言是平台级基础数据，登录用户可读）。
		{Role: "viewer", Object: "language", Action: "get"},
		{Role: "viewer", Object: "language", Action: "list"},
		// Wave 6.2 管理面聚合（AdminPortalService）：object 用 **admin_portal**
		// （自定义 resolver 产出，见 handler/gin/admin_portal.go）——**不能用
		// "admin"**：/admin/components 的管理面组件目录也归一化为 "admin"，
		// 给 viewer 开 admin:get 会连带放开它（实测回归：TestAdmin_Forbidden
		// 由 403 变 200）。独立 object 使导航/权限码的只读开放与组件目录的
		// admin 专属隔离。
		{Role: "admin", Object: "admin_portal", Action: "get"},
		{Role: "admin", Object: "admin_portal", Action: "list"},
		// viewer 需拉导航/权限码（前端初始化），故只读开放。
		{Role: "viewer", Object: "admin_portal", Action: "get"},
		{Role: "viewer", Object: "admin_portal", Action: "list"},
		// Wave 6.2 缓存监控（RedisCacheMonitorService）：admin 专属运维接口。
		{Role: "admin", Object: "redis-cache-monitor", Action: "get"},
		{Role: "admin", Object: "redis-cache-monitor", Action: "list"},
		// admin：文件管理（T5）。同款六元组（REST HTTP 动词小写 + gRPC
		// get/list/write 双协议全放行；DefaultGRPCObject("FileService/...")="file"）。
		{Role: "admin", Object: "file", Action: "get"},
		{Role: "admin", Object: "file", Action: "post"},
		{Role: "admin", Object: "file", Action: "delete"},
		{Role: "admin", Object: "file", Action: "list"},
		{Role: "admin", Object: "file", Action: "write"},
		// viewer：文件只读（T5；上传/删除仅 admin）。
		{Role: "viewer", Object: "file", Action: "get"},
		{Role: "viewer", Object: "file", Action: "list"},
		// admin：审计查询（T6，审计数据敏感仅 admin；REST /v1/audit 与 gRPC
		// AuditService 同源归一化 "audit"）。
		{Role: "admin", Object: "audit", Action: "get"},
		{Role: "admin", Object: "audit", Action: "list"},
	}
	for i := range policies {
		p := policies[i]
		p.ID = p.Role + ":" + p.Object + ":" + p.Action
		if err := RolePolicyStore.Create(ctx, &p); err != nil && err != store.ErrConflict {
			return err
		}
	}
	return nil
}

// 注册外部 SQL 后端的 gorm dialector（注册制：未 import 的后端不进依赖树，
// 与 registry 各后端契约装配同模式；sqlite 由 contrib/store-gorm 预注册为缺省引擎，
// 纯 Go driver 零 CGO——本机/CI 无 gcc 环境可跑）。
func init() {
	baldgorm.RegisterDialector("postgres", postgres.Open)
	baldgorm.RegisterDialector("postgresql", postgres.Open)
	baldgorm.RegisterDialector("mysql", mysql.Open)
}

// openDB 打开应用主库（装配逻辑上提 contrib/store-gorm 的 Open，本函数只保留
// 应用特有的来源约定）：
//   - DSN 优先级：env BALD_ADMIN_DB_DSN > 契约段 database.sql > SQLite 内存库
//     （零外部依赖，便于快速开发与 e2e）；
//   - 驱动优先级：契约段 driver 字段 > DSN scheme 推断；未注册驱动 fail-fast；
//   - 契约段连接池参数（max_idle/max_open/lifetime）一并消费。
//
// U1 起 sqlCfg 由调用方传入（DatabaseProvider 直收契约段；InitBridges 旧路径
// 传 depsBootstrap 的段），nil = 无契约段（env/内存库路径）。
//
// 注意：本函数返回的连接用于 AutoMigrate + seed；多连接场景 SQLite 内存库须用
// cache=shared 且 keep 一个引用，否则其他连接读到空库。
func openDB(sqlCfg *bootstrapv1.Database_SQL) (*gorm.DB, error) {
	opts := []baldgorm.Option{baldgorm.WithEnv("BALD_ADMIN_DB_DSN")}
	if sqlCfg != nil && sqlCfg.GetSource() != "" {
		opts = append(opts, baldgorm.WithConfig(sqlCfg))
	}
	return baldgorm.Open(opts...)
}

// resolveRedis 解析 Redis 连接参数：env BALD_ADMIN_REDIS_ADDR 优先（覆盖手段），
// 其次配置段 cache.redis（addr/password/db）；两者皆空返回空 addr（禁用态，
// BuildRedisCache 返回 nil，调用方降级直连）。
// D1：返回三元组而非 []rediscache.Option——新 cache/redis 适配器只收已构造的
// client，连接参数在 BuildRedisCache 内落到 goredis.Options。
func resolveRedis() (addr, password string, db int) {
	if a := os.Getenv("BALD_ADMIN_REDIS_ADDR"); a != "" {
		return a, "", 0
	}
	rc := depsBootstrap.GetCache().GetRedis()
	if rc == nil || rc.GetAddr() == "" {
		return "", "", 0
	}
	return rc.GetAddr(), rc.GetPassword(), int(rc.GetDb())
}

// dsnScheme 的解析逻辑已上提 contrib/store-gorm（Open 的 scheme 推断）。

// loadPolicyCSV 从 DB 装载 casbin 策略（D3 策略数据化）：
//   - p 行：RolePolicy 表全量（role,object,action 三元组）；
//   - g 行：UserStore 全量用户的 Roles 字段展开（subject→角色绑定）。
//
// 返回 csv 文本注入 contrib casbin。空库 → 空 csv → casbin 默认拒绝（fail-closed），
// 即「无策略时全部请求 403」——授权不因缺数据而放开。
func loadPolicyCSV(ctx context.Context) (string, error) {
	policies, _, err := RolePolicyStore.List(ctx, &store.Where{})
	if err != nil {
		return "", fmt.Errorf("bootstrap: load role policies: %w", err)
	}
	users, _, err := UserStore.List(ctx, &store.Where{})
	if err != nil {
		return "", fmt.Errorf("bootstrap: load users for policy: %w", err)
	}
	var sb strings.Builder
	for _, p := range policies {
		fmt.Fprintf(&sb, "p, %s, %s, %s\n", p.Role, p.Object, p.Action)
	}
	for _, u := range users {
		for _, r := range u.RolesList() {
			fmt.Fprintf(&sb, "g, %s, %s\n", u.ID, r)
		}
	}
	return sb.String(), nil
}

// loadRBACMaps 已从 bootstrap 移除（M6.1）：RBAC 策略改由 casbin 桥接加载；
// T3 起进一步数据化——p 行 RolePolicy 表、g 行 User.Roles（见 loadPolicyCSV）。
