package audit

// record_mapper.go —— Wave 5.1：审计事件 → authmodel.AuditRecord 的业务映射。
//
// ## 为何需要自定义映射
//
// 框架 `contrib/audit-store` 的默认映射（`defaultRecord`）只填 12 个基础列。
// Wave 5.1 为承载源 go-wind-admin 的**五类审计**（operation/login/api/
// data_access/permission）在 `authmodel.AuditRecord` 上扩了 15 个 nullable 列
// （见 model/audit.go）。本映射把 `AuditEvent.Meta` 里的分类专属字段提取到
// 对应列——**零框架改动**（`auditstore.WithRecordMapper` 是框架提供的扩展点）。
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
	"github.com/kalandramo/bald/pkg/audit"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

// RecordMapper 是事件 → 业务审计表记录的映射（供 auditstore.WithRecordMapper）。
//
// 返回值须为 `*authmodel.AuditRecord`——`bootstrap` 的 AutoMigrate 已迁移该表
// （`internal/bootstrap/bootstrap.go:295`），列结构与之一致。
func RecordMapper(ev audit.AuditEvent) any {
	return &authmodel.AuditRecord{
		TenantID: ev.TenantID,
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
