package model

// PermissionGroup 权限组。
//
// **树形结构**：用物化路径（materialized path）——`Path` 格式
// `/1/10/101/`（**含自身、首尾带 `/`**）。org 域的 `Path`（`model/org.go`）
// 同为物化路径但无尾斜杠；本模型统一用首尾带 `/` 的格式。
type PermissionGroup struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pg:<code>"
	TenantID string `gorm:"index"`
	// Name 分组名称（如：用户管理、订单操作）。
	Name string
	// Path 物化路径，格式 `/1/10/101/`（含自身、首尾带 /）。
	Path string `gorm:"index"`
	// Module 业务模块标识（如：opm、order、pay）。
	Module string `gorm:"index"`
	// ParentID 父节点 ID（空 = 根节点）。
	ParentID string `gorm:"index"`
	// Status ON / OFF。
	Status string
	// SortOrder 排序。
	SortOrder int32
	// Remark 备注。
	Remark string
}

// PolicyEvaluationLog 策略评估日志。
//
// 语义：每次权限判定（casbin 评估）落一条——记录「谁、对什么、请求哪个 API、
// 结果如何、为何拒绝」。字段只保留项目实际可得的数据
// （ID 用字符串业务键）。
type PolicyEvaluationLog struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:pel:<seq>"
	TenantID string `gorm:"index"`
	// UserID 操作者用户 ID。
	UserID string `gorm:"index"`
	// PermissionID 权限点 ID（本项目为权限码）。
	PermissionID string `gorm:"index"`
	// PolicyID 命中的策略 ID（可能无策略）。
	PolicyID string
	// RequestPath 请求 API 路径。
	RequestPath string
	// RequestMethod 请求 HTTP 方法。
	RequestMethod string
	// Result 是否通过。
	Result bool `gorm:"index"`
	// EffectDetails 评估详情 / 拒绝原因。
	EffectDetails string
	// ScopeSQL 生成的 SQL 条件。
	ScopeSQL string
	// CreatedAt 评估时刻（UnixNano）。
	CreatedAt int64 `gorm:"index"`
}
