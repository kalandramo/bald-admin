package apiserver

import (
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	cmbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/cachemonitor"
	dashbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dashboard"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	identitybiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/identity"
	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
	mfabiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/mfa"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	pgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permgroup"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	planbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/plan"
	portalbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/portal"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
)

// BizSet 聚合本应用全部业务对象：wire 装配产物、RegisterRoutes 的入参。
//
// T10 目录架构对齐：吸收 miniblog IBiz 门面思路但不引入接口层——struct 直传，
// 与 §0「外部依赖禁止 fake/mock/stub」契约一致（无 mockgen 场景即无需接口抽象）。
// 新增域只改两处：本结构扩一个字段 + RegisterRoutes 挂一行 handler
// （此前 wire.go / wire_gen.go / server.go 三处签名联动）。
//
// 批 4 域形态迁移进行中（混合态）：已迁域（tenant/user，其余 19 域随批跟进）
// 的生产构造移交域声明包（internal/apiserver/domains/<d> 的 Wire），本结构
// 对应字段退化为 handler 级 e2e 的直构接线位——生产装配不填充（nil），
// RegisterRoutes 的 nil 守卫跳过、域 Routes 承担注册。全部迁完后本结构与
// RegisterRoutes 的逐域行一并溶解。
type BizSet struct {
	Auth       *authbiz.Biz
	Secret     *secretbiz.SecretBiz
	// Tenant/User（批 4 混合态）：生产构造已迁往 domains/tenantd、domains/userd
	// 的域 Wire；字段保留仅为 handler 级 e2e 直构接线，生产装配不填充（nil）。
	Tenant     *tenantbiz.Biz
	User       *userbiz.Biz
	Menu       *menubiz.Biz
	Permission *permissionbiz.Biz
	Dict       *dictbiz.Biz
	File       *filebiz.Biz
	AuditLog   *auditlogbiz.Biz
	// MFA 多因素认证。
	MFA *mfabiz.Biz
	// Identity identity 扩展域（credential + login_policy）。
	Identity *identitybiz.Biz
	// Org 组织架构（org_unit 树 + position）。
	Org *orgbiz.Biz
	// Task 任务调度。
	Task *taskbiz.Biz
	// Message 站内消息。
	Message *msgbiz.Biz
	// Dashboard 首页分析。
	Dashboard *dashbiz.Biz
	// PermGroup 权限组 + 策略评估日志。
	PermGroup *pgbiz.Biz
	// Plan 套餐三件套。
	Plan *planbiz.Biz
	// Language 语言管理（；平台级数据）。
	Language *langbiz.Biz
	// Portal 管理面聚合（；AdminPortalService 3 rpc）。
	Portal *portalbiz.Biz
	// CacheMonitor Redis 缓存监控（；1 rpc）。
	CacheMonitor *cmbiz.Biz
}
