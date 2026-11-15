// Package seed 承载授权策略行（bald gen app 生成一次，此后归你维护；
// 重生成跳过本文件）。
//
// 默认策略从 proto 的 service/method 派生（归一化口径与 pkg/authz 同源）：
// 每个 service 一个 object = Service 段去 Service 后缀转小写
// （DefaultGRPCObject）；动作 = **六元组**——REST 动词（get/post/put/delete）+
// gRPC 动作（list/write）双面对齐。教训：只写四条 REST 动词，补 gRPC 面时
// List(→list)/Create(→write) 因缺策略行 403。viewer 只读
// （get/list）；http_only 域无 proto service，不派生策略行。
// gen domain 产物 authz/alias.gen.go 的 DefaultActions 是本表的对账面。只有
// 自定义策略才需要登记在本文件。
//
// 消费入口（行为不在生成物里）：contrib/authz-casbin 的 NewRows——本表行
// 映射为 authzcasbin.PolicyRow 装载（见该包文档；角色绑定 g 行是运行期
// 业务数据，由业务侧提供）。
package seed

// PolicyRow 一条默认授权策略行（role/object/action）。
type PolicyRow struct {
	Role   string
	Object string
	Action string
}

// Rows 是默认授权策略行（AppSpec.domains 声明序；object 由域 service 派生）。
var Rows = []PolicyRow{
	// tenant（go.bald.admin.tenant.v1.TenantService → object tenant）
	{Role: "admin", Object: "tenant", Action: "get"},
	{Role: "admin", Object: "tenant", Action: "list"},
	{Role: "admin", Object: "tenant", Action: "write"},
	{Role: "admin", Object: "tenant", Action: "delete"},
	{Role: "admin", Object: "tenant", Action: "post"},
	{Role: "admin", Object: "tenant", Action: "put"},
	{Role: "viewer", Object: "tenant", Action: "get"},
	{Role: "viewer", Object: "tenant", Action: "list"},
	// user（go.bald.admin.user.v1.UserService → object user）
	{Role: "admin", Object: "user", Action: "get"},
	{Role: "admin", Object: "user", Action: "list"},
	{Role: "admin", Object: "user", Action: "write"},
	{Role: "admin", Object: "user", Action: "delete"},
	{Role: "admin", Object: "user", Action: "post"},
	{Role: "admin", Object: "user", Action: "put"},
	{Role: "viewer", Object: "user", Action: "get"},
	{Role: "viewer", Object: "user", Action: "list"},
	// secret（go.bald.admin.v1.SecretService → object secret）
	{Role: "admin", Object: "secret", Action: "get"},
	{Role: "admin", Object: "secret", Action: "list"},
	{Role: "admin", Object: "secret", Action: "write"},
	{Role: "admin", Object: "secret", Action: "delete"},
	{Role: "admin", Object: "secret", Action: "post"},
	{Role: "admin", Object: "secret", Action: "put"},
	{Role: "viewer", Object: "secret", Action: "get"},
	{Role: "viewer", Object: "secret", Action: "list"},
	// menu（go.bald.admin.menu.v1.MenuService → object menu）
	{Role: "admin", Object: "menu", Action: "get"},
	{Role: "admin", Object: "menu", Action: "list"},
	{Role: "admin", Object: "menu", Action: "write"},
	{Role: "admin", Object: "menu", Action: "delete"},
	{Role: "admin", Object: "menu", Action: "post"},
	{Role: "admin", Object: "menu", Action: "put"},
	{Role: "viewer", Object: "menu", Action: "get"},
	{Role: "viewer", Object: "menu", Action: "list"},
	// permission（go.bald.admin.permission.v1.PermissionService → object permission）
	{Role: "admin", Object: "permission", Action: "get"},
	{Role: "admin", Object: "permission", Action: "list"},
	{Role: "admin", Object: "permission", Action: "write"},
	{Role: "admin", Object: "permission", Action: "delete"},
	{Role: "admin", Object: "permission", Action: "post"},
	{Role: "admin", Object: "permission", Action: "put"},
	{Role: "viewer", Object: "permission", Action: "get"},
	{Role: "viewer", Object: "permission", Action: "list"},
	// dict（go.bald.admin.dict.v1.DictTypeService → object dicttype）
	{Role: "admin", Object: "dicttype", Action: "get"},
	{Role: "admin", Object: "dicttype", Action: "list"},
	{Role: "admin", Object: "dicttype", Action: "write"},
	{Role: "admin", Object: "dicttype", Action: "delete"},
	{Role: "admin", Object: "dicttype", Action: "post"},
	{Role: "admin", Object: "dicttype", Action: "put"},
	{Role: "viewer", Object: "dicttype", Action: "get"},
	{Role: "viewer", Object: "dicttype", Action: "list"},
	// dict（go.bald.admin.dict.v1.DictEntryService → object dictentry）
	{Role: "admin", Object: "dictentry", Action: "get"},
	{Role: "admin", Object: "dictentry", Action: "list"},
	{Role: "admin", Object: "dictentry", Action: "write"},
	{Role: "admin", Object: "dictentry", Action: "delete"},
	{Role: "admin", Object: "dictentry", Action: "post"},
	{Role: "admin", Object: "dictentry", Action: "put"},
	{Role: "viewer", Object: "dictentry", Action: "get"},
	{Role: "viewer", Object: "dictentry", Action: "list"},
	// file（go.bald.admin.file.v1.FileService → object file）
	{Role: "admin", Object: "file", Action: "get"},
	{Role: "admin", Object: "file", Action: "list"},
	{Role: "admin", Object: "file", Action: "write"},
	{Role: "admin", Object: "file", Action: "delete"},
	{Role: "admin", Object: "file", Action: "post"},
	{Role: "admin", Object: "file", Action: "put"},
	{Role: "viewer", Object: "file", Action: "get"},
	{Role: "viewer", Object: "file", Action: "list"},
	// auditlog（go.bald.admin.audit.v1.AuditService → object audit）
	{Role: "admin", Object: "audit", Action: "get"},
	{Role: "admin", Object: "audit", Action: "list"},
	{Role: "admin", Object: "audit", Action: "write"},
	{Role: "admin", Object: "audit", Action: "delete"},
	{Role: "admin", Object: "audit", Action: "post"},
	{Role: "admin", Object: "audit", Action: "put"},
	{Role: "viewer", Object: "audit", Action: "get"},
	{Role: "viewer", Object: "audit", Action: "list"},
	// org（go.bald.admin.identity.v1.OrgUnitService → object orgunit）
	{Role: "admin", Object: "orgunit", Action: "get"},
	{Role: "admin", Object: "orgunit", Action: "list"},
	{Role: "admin", Object: "orgunit", Action: "write"},
	{Role: "admin", Object: "orgunit", Action: "delete"},
	{Role: "admin", Object: "orgunit", Action: "post"},
	{Role: "admin", Object: "orgunit", Action: "put"},
	{Role: "viewer", Object: "orgunit", Action: "get"},
	{Role: "viewer", Object: "orgunit", Action: "list"},
	// org（go.bald.admin.identity.v1.PositionService → object position）
	{Role: "admin", Object: "position", Action: "get"},
	{Role: "admin", Object: "position", Action: "list"},
	{Role: "admin", Object: "position", Action: "write"},
	{Role: "admin", Object: "position", Action: "delete"},
	{Role: "admin", Object: "position", Action: "post"},
	{Role: "admin", Object: "position", Action: "put"},
	{Role: "viewer", Object: "position", Action: "get"},
	{Role: "viewer", Object: "position", Action: "list"},
	// language（go.bald.admin.dict.v1.LanguageService → object language）
	{Role: "admin", Object: "language", Action: "get"},
	{Role: "admin", Object: "language", Action: "list"},
	{Role: "admin", Object: "language", Action: "write"},
	{Role: "admin", Object: "language", Action: "delete"},
	{Role: "admin", Object: "language", Action: "post"},
	{Role: "admin", Object: "language", Action: "put"},
	{Role: "viewer", Object: "language", Action: "get"},
	{Role: "viewer", Object: "language", Action: "list"},
	// portal（go.bald.admin.admin.v1.AdminPortalService → object adminportal）
	{Role: "admin", Object: "adminportal", Action: "get"},
	{Role: "admin", Object: "adminportal", Action: "list"},
	{Role: "admin", Object: "adminportal", Action: "write"},
	{Role: "admin", Object: "adminportal", Action: "delete"},
	{Role: "admin", Object: "adminportal", Action: "post"},
	{Role: "admin", Object: "adminportal", Action: "put"},
	{Role: "viewer", Object: "adminportal", Action: "get"},
	{Role: "viewer", Object: "adminportal", Action: "list"},
	// cachemonitor（go.bald.admin.admin.v1.RedisCacheMonitorService → object rediscachemonitor）
	{Role: "admin", Object: "rediscachemonitor", Action: "get"},
	{Role: "admin", Object: "rediscachemonitor", Action: "list"},
	{Role: "admin", Object: "rediscachemonitor", Action: "write"},
	{Role: "admin", Object: "rediscachemonitor", Action: "delete"},
	{Role: "admin", Object: "rediscachemonitor", Action: "post"},
	{Role: "admin", Object: "rediscachemonitor", Action: "put"},
	{Role: "viewer", Object: "rediscachemonitor", Action: "get"},
	{Role: "viewer", Object: "rediscachemonitor", Action: "list"},
	// identity: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// dashboard: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// message: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// mfa: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// permgroup: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// plan: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
	// task: http_only domain (REST surface is owned by the domain package; no proto service, so no policy rows are derived)
}
