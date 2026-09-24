package audit

// record_mapper.go —— Wave 5.1：审计事件 → authmodel.AuditRecord 的业务映射。
//
// ## 为何需要自定义映射
//
// 框架 `contrib/audit-gorm` 的默认映射（`defaultRecord`）只填 12 个基础列。
// Wave 5.1 为承载源 go-wind-admin 的**五类审计**（operation/login/api/
// data_access/permission）在 `authmodel.AuditRecord` 上扩了 15 个 nullable 列
// （见 model/audit.go）。本映射把 `AuditEvent.Meta` 里的分类专属字段提取到
// 对应列——**零框架改动**（`auditgorm.WithRecordMapper` 是框架提供的扩展点）。
//
// ## 为何下沉到本包（而非留在 cmd/go-bald-admin）
//
// 生产路径（cmd/go-bald-admin/audit_wiring.go 的 lazyStoreAuditor）与 e2e
// 路径（t6/t_w5_1 用 securityaudit.NewStore）**必须共用同一映射**——否则
// 两条路径落库的列不同，e2e 测不出生产行为（分叉即静默失效）。本包是审计
// 桥接层，是两条路径的公共汇合点，映射放这里最自然。
//
// 列名靠 gorm 默认蛇形映射（`HTTPMethod` → `http_method` 等），与框架默认
// 模型的 12 列同名同列，故 `AutoMigrate` 无冲突。

import (
	"sync"

	"github.com/kalandramo/bald/pkg/audit"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

// DefaultTenantID 是审计事件缺租户时的**兜底默认值**。
//
// 为什么需要兜底（2026-09-22）：审计查询改为租户隔离后（走 ListWithPaging →
// mergeTenant 自动注入 tenant_id 条件），落库时 TenantID 为空的事件**查不到**。
// 有三类事件天然拿不到租户：
//   - login 失败（用户不存在/限流/熔断——无从得知用户所属租户）；
//   - permission 变更（auditPermission 未设 TenantID）；
//   - data_access（sqlaudit gorm 插件采集，事件里无租户）。
//
// 这些恰是审计最需要的线索（谁在试密码 / 谁改了权限 / 谁访问了敏感表）。
// 故在**落库汇聚点**统一兜底——RecordMapper 是所有审计事件的必经之路，
// 一处覆盖全部调用方（优于在各调用点逐个打补丁）。
//
// **可配置**（2026-09-22 后续）：本值仅为缺省，生产经配置段
// `audit.fallback_tenant` 覆盖（见 SetFallbackTenant）；未配置时用本常量。
// 用户名/动作等线索记在 Subject/Action 等独立字段，不受此兜底影响。
const DefaultTenantID = "t-default"

// fallbackTenant 是当前生效的兜底租户（经 SetFallbackTenant 可覆盖）。
//
// 用 sync.RWMutex 保护：SetFallbackTenant 在启动期（main → Configure）调用，
// RecordMapper 在请求期并发读取（审计写入是多 goroutine 的）——启动期写入
// 与请求期读取之间存在 happens-before 边界，但显式加锁让契约更清晰、
// 也允许未来运行期改配置（如配置热重载）。
var (
	fallbackMu     sync.RWMutex
	fallbackTenant = DefaultTenantID
)

// SetFallbackTenant 设置审计兜底租户（空串 → 回落 DefaultTenantID）。
//
// 由 cmd/go-bald-admin 在启动期从配置段 `audit.fallback_tenant` 读取后注入。
// 未调用时保持 DefaultTenantID（既有行为零改动）。
func SetFallbackTenant(id string) {
	if id == "" {
		id = DefaultTenantID
	}
	fallbackMu.Lock()
	fallbackTenant = id
	fallbackMu.Unlock()
}

// currentFallbackTenant 读取当前兜底租户（并发安全）。
func currentFallbackTenant() string {
	fallbackMu.RLock()
	defer fallbackMu.RUnlock()
	return fallbackTenant
}

// NewRecordMapper 返回一个把审计事件映射为 authmodel.AuditRecord 的映射函数
// （供 auditgorm.WithRecordMapper）。
//
// **为什么是工厂而非直接暴露函数**：框架的 `WithRecordMapper` 签名是
// `func(audit.AuditEvent) any`——没有配置注入位。工厂模式让调用方在构造期
// 决定兜底租户，而框架侧零改动。
//
// 不传参（或传空）时用当前生效的兜底租户（SetFallbackTenant 设定，缺省
// DefaultTenantID）——这样调用方无需重复读配置。
//
// 返回值须为 `*authmodel.AuditRecord`——`bootstrap` 的 AutoMigrate 已迁移该表
// （`internal/bootstrap/bootstrap.go:295`），列结构与之一致。
func NewRecordMapper(fallbackTenantID string) func(audit.AuditEvent) any {
	if fallbackTenantID == "" {
		fallbackTenantID = currentFallbackTenant()
	}
	return func(ev audit.AuditEvent) any {
		tenantID := ev.TenantID
		if tenantID == "" {
			tenantID = fallbackTenantID // 见 DefaultTenantID 注释
		}
		return &authmodel.AuditRecord{
			TenantID: tenantID,
			Time:     ev.Time.UnixNano(),
			Subject:  ev.Subject,
			Object:   ev.Object,
			Action:   ev.Action,
			Result:   string(ev.Result),
			Error:    ev.Error,

			Category:  metaStr(ev.Meta, "category", "operation"),
			IPAddress: metaStr(ev.Meta, "client_ip", ""),
			UserAgent: metaStr(ev.Meta, "user_agent", ""),
			RequestID: metaStr(ev.Meta, "request_id", ""),
			TraceID:   metaStr(ev.Meta, "trace_id", ""),

			// api 类（源 ApiAuditLog）。
			HTTPMethod: metaStr(ev.Meta, "http_method", ""),
			Path:       metaStr(ev.Meta, "path", ""),
			StatusCode: metaUint32(ev.Meta, "status"),
			LatencyMs:  metaUint32(ev.Meta, "latency_ms"),

			// data_access 类（源 DataAccessAuditLog）。
			TableName:    metaStr(ev.Meta, "table_name", ""),
			DataSource:   metaStr(ev.Meta, "data_source", ""),
			DBUser:       metaStr(ev.Meta, "db_user", ""),
			SQLText:      metaStr(ev.Meta, "sql_text", ""),
			AffectedRows: metaUint32(ev.Meta, "affected_rows"),

			// permission 类（源 PermissionAuditLog）。
			TargetType: metaStr(ev.Meta, "target_type", ""),
			TargetID:   metaStr(ev.Meta, "target_id", ""),
			OldValue:   metaStr(ev.Meta, "old_value", ""),
			NewValue:   metaStr(ev.Meta, "new_value", ""),

			// login 类（源 LoginAuditLog）。
			SessionID: metaStr(ev.Meta, "session_id", ""),
			MFAStatus: metaStr(ev.Meta, "mfa_status", ""),
		}
	}
}

// 用法：消费方在**构造期**调用 NewRecordMapper("") 取值（此时
// SetFallbackTenant 已由启动流程执行完毕）。
//
// 注意：**故意不提供包级 `RecordMapper` 变量**——包级变量在 init 期求值，
// 会固化 SetFallbackTenant 调用之前的值，导致配置「看起来能配却不生效」
// （静默失效比不提供更糟）。消费方显式调用工厂，语义无歧义。

// metaStr 从 Meta 取字符串值；缺键/类型不符返回 def。
func metaStr(meta map[string]any, key, def string) string {
	if meta == nil {
		return def
	}
	if v, ok := meta[key].(string); ok && v != "" {
		return v
	}
	return def
}

// metaUint32 从 Meta 取非负整数（兼容 int/int64/uint32/uint64/float64 等
// JSON 数值形态——框架中间件注入的 status 是 int，JSON 反序列化是 float64）。
func metaUint32(meta map[string]any, key string) uint32 {
	if meta == nil {
		return 0
	}
	switch v := meta[key].(type) {
	case int:
		if v > 0 {
			return uint32(v)
		}
	case int64:
		if v > 0 {
			return uint32(v)
		}
	case uint32:
		return v
	case uint64:
		return uint32(v)
	case float64:
		if v > 0 {
			return uint32(v)
		}
	}
	return 0
}
