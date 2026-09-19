package model

// Plan 套餐（Wave 4.2，自源 identity/service/v1/plan.proto 精简移植）。
//
// 三件套的**父实体**：Plan（套餐）→ PlanModule（含哪些模块）+
// PlanQuota（各项配额）。源用 ent 的 Edge 关系
// （`plan.go:93,100` 的 `OnDelete: entsql.Cascade`）——删套餐时
// DB 层自动级联删子表。本项目无 ent，故在 biz 层**手工级联**
// （与 message 域 `DeleteMessage` 同款，见 `biz/v1/message/message.go`）。
type Plan struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:plan:<code>"
	TenantID string `gorm:"index"`
	// Name 套餐名称。
	Name string
	// Version 版本标识（源 Version 枚举：基础版/专业版/企业版）。
	Version string
	// ExpiryPolicy 到期策略（源 ExpiryPolicy 枚举）。
	ExpiryPolicy string
	// DataRetentionDays 数据保留天数（源 data_retention_days）。
	DataRetentionDays int32
	// Status ON / OFF。
	Status string
	// Remark 备注。
	Remark string
}

// PlanModule 套餐包含的模块（Wave 4.2）。
//
// 源实体（`plan_module.proto`）：`plan_id` + `module`（Module 枚举）。
// 本项目用字符串标识模块（与 menu/权限码同风格）。
type PlanModule struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pm:<plan>:<module>"
	TenantID string `gorm:"index"`
	// PlanID 所属套餐 ID（源 plan_id）。
	PlanID string `gorm:"index"`
	// Module 模块标识（源 Module 枚举）。
	Module string `gorm:"index"`
}

// PlanQuota 套餐配额项（Wave 4.2）。
//
// 源实体（`plan_quota.proto`）：`plan_id` + `quota_type`（QuotaType 枚举）
// + `quota_value`（uint64）。
type PlanQuota struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pq:<plan>:<quotaType>"
	TenantID string `gorm:"index"`
	// PlanID 所属套餐 ID（源 plan_id）。
	PlanID string `gorm:"index"`
	// QuotaType 配额类型（源 QuotaType 枚举，如 max_users / max_storage）。
	QuotaType string `gorm:"index"`
	// QuotaValue 配额值（源 quota_value，uint64）。
	QuotaValue uint64
}
