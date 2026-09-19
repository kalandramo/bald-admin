package model

import "time"

// OrgUnit 组织单元（Wave 1.7，自源 org_unit.proto 精简移植）。
//
// **树形结构**：用 ParentID 表达父子关系，Path 存物化路径（如 "/1/3/7"）
// 供快速子树查询——源的 `path` 字段语义（proto:5「树路径」）。
//
// 主键用业务键 `"<tenant>:<code>"`：源的 code 是「唯一编码（建议规则：
// 部门编码 + 组织单元类型 + 序号）」（proto:3），是跨系统同步的稳定标识。
type OrgUnit struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:<code>"
	TenantID string `gorm:"index"`
	Name     string
	Code     string `gorm:"index"`
	Type     string // COMPANY / DEPARTMENT / TEAM / GROUP ...
	// ParentID 父节点 ID（空 = 根节点）。
	ParentID string `gorm:"index"`
	// Path 物化路径（如 "/root-id/child-id"），供子树查询。
	Path      string
	Status    string // ON / OFF
	SortOrder int32
	// LeaderID 负责人用户 ID（回填时从 User 表取 username → LeaderName）。
	LeaderID   string
	LeaderName string `gorm:"-"` // 非持久化：查询时回填（源同此，proto:11）
	Remark     string
	Description string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Position 职位（Wave 1.7，自源 position.proto 精简移植）。
//
// 与 OrgUnit 的关联：OrgUnitID 指向所属组织单元，回填 OrgUnitName（proto:23）；
// ReportsToPositionID 指向汇报上级职位，回填 ReportsToPositionName（proto:25）。
type Position struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:<code>"
	TenantID string `gorm:"index"`
	Name     string
	Code     string `gorm:"index"`
	Headcount int32
	SortOrder int32
	Status    string
	Type      string // 职位类型
	Remark    string
	Description string
	JobFamily string
	JobGrade  string
	Level     int32
	IsKeyPosition bool
	// OrgUnitID 所属组织单元（回填 OrgUnitName）。
	OrgUnitID   string `gorm:"index"`
	OrgUnitName string `gorm:"-"` // 非持久化：查询时回填
	// ReportsToPositionID 汇报上级职位（回填 ReportsToPositionName）。
	ReportsToPositionID   string
	ReportsToPositionName string `gorm:"-"`
	StartAt   *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}
