package model

import "time"

// Tenant 业务租户。
// ID 即租户编码（如 "platform"、"t-acme"），与 store 层的 tenant_id 同一命名空间：
// 业务租户创建后即可作为 users/secrets 等业务表的隔离维度值使用。
//
// 刻意不设 TenantID 字段——本表是「租户即业务实体」的平台侧管理面：写路径注入
// （injectWriteTenant 对无 TenantID 字段实体静默跳过）与读过滤（查询不调 Where.T）
// 都天然不作用于它；全量读写即平台语义。
type Tenant struct {
	ID        string `gorm:"primaryKey"` // 租户编码（= tenant_id）
	Name      string // 展示名
	Status    string // "ON"/"OFF"/"FREEZE"
	Remark    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
