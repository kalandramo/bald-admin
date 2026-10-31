package model

// Plan 套餐。
//
// 三件套的**父实体**：Plan（套餐）→ PlanModule（含哪些模块）+
// PlanQuota（各项配额）。删套餐时需级联删除子表；本项目无 ORM 级联
// 能力，故在 biz 层**手工级联**（与 message 域 `DeleteMessage` 同款，
// 见 `biz/v1/message/message.go`）。
type Plan struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:plan:<code>"
	TenantID string `gorm:"index"`
	// Name 套餐名称。
	Name string
	// Version 版本标识（基础版/专业版/企业版）。
	Version string
	// ExpiryPolicy 到期策略。
	ExpiryPolicy string
	// DataRetentionDays 数据保留天数。
	DataRetentionDays int32
	// Status ON / OFF。
	Status string
	// Remark 备注。
	Remark string
}

// PlanModule 套餐包含的模块。
//
// 用 `plan_id` + `module` 表达归属；模块以字符串标识
// （与 menu/权限码同风格）。
type PlanModule struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pm:<plan>:<module>"
	TenantID string `gorm:"index"`
	// PlanID 所属套餐 ID。
	PlanID string `gorm:"index"`
	// Module 模块标识。
	Module string `gorm:"index"`
}

// PlanQuota 套餐配额项。
//
// 用 `plan_id` + `quota_type` 标识，`quota_value`（uint64）承载配额值。
type PlanQuota struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pq:<plan>:<quotaType>"
	TenantID string `gorm:"index"`
	// PlanID 所属套餐 ID。
	PlanID string `gorm:"index"`
	// QuotaType 配额类型（如 max_users / max_storage）。
	QuotaType string `gorm:"index"`
	// QuotaValue 配额值（uint64）。
	QuotaValue uint64
}
