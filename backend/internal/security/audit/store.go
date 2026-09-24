package audit

import (
	"gorm.io/gorm"

	"github.com/kalandramo/bald/contrib/audit-gorm"
	"github.com/kalandramo/bald/pkg/audit"
)

// NewStore 构造落库审计后端（转发 contrib/audit-gorm）。
//
// 框架默认表模型与既有 authmodel.AuditRecord 的 12 个基础列字段/列名对齐
// （同一 audit_records 表，migrate 无冲突）；落库失败/panic 降级 fallback
// 双写语义不变。
//
// Wave 5.1：注入 `RecordMapper` 把 Meta 里的五类分类专属字段（api/
// data_access/permission/login）提取到 `authmodel.AuditRecord` 的扩展列——
// **生产路径（audit_wiring.go）与 e2e 路径共用同一映射**，避免两条路径
// 落库列不同导致 e2e 测不出生产行为（见 record_mapper.go 头注）。
//
// 2026-09-22：映射改为构造期求值的工厂（NewRecordMapper），使兜底租户可经
// SetFallbackTenant 配置。此处传空串 → 取当前生效的兜底租户。
func NewStore(db *gorm.DB) audit.Auditor {
	return auditgorm.New(db, auditgorm.WithRecordMapper(NewRecordMapper("")))
}
