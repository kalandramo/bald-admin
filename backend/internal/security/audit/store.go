package audit

import (
	"gorm.io/gorm"

	"github.com/kalandramo/bald/contrib/audit-store"
	"github.com/kalandramo/bald/pkg/audit"
)

// NewStore 构造落库审计后端（转发 contrib/audit-store）。
//
// 框架默认表模型与既有 authmodel.AuditRecord 字段/列名完全对齐（同一
// audit_records 表，migrate 无冲突）；落库失败/panic 降级 fallback 双写
// 语义不变。表结构不同的场景用 auditstore.WithRecordMapper。
func NewStore(db *gorm.DB) audit.Auditor { return auditstore.New(db) }
