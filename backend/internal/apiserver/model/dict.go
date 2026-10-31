package model

import "time"

// DictType 字典类型。ID 即类型编码（immutable 语义——业务键做主键，
// 同 Menu/Permission 范式）。
// 带 TenantID 字段：字典是租户级业务数据，store 层自动隔离；
// Cache-Aside 键亦含租户维度（dict biz）。
type DictType struct {
	ID        string `gorm:"primaryKey"` // 类型编码（如 "gender"）
	TenantID  string `gorm:"index"`
	TypeName  string // 显示名称
	SortOrder int32  // 展示顺序
	Enabled   bool   // 启用
	Remark    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DictEntry 字典项。主键 = "<type_code>:<entry_value>" 业务键（同 RolePolicy
// 范式：Create 冲突即条目重复，「同租户同类型 entry_value 唯一」约束的等价实现
// ——type_code/value 不可变，Update 仅改展示属性）。Label 内联 i18n 中文条目
// （仅 zh，未做多语言表）；Numeric 是可空数值。
type DictEntry struct {
	ID        string `gorm:"primaryKey"` // 业务键 "<type_code>:<entry_value>"
	TenantID  string `gorm:"index"`
	TypeCode  string `gorm:"index"` // 所属类型编码（用编码引用而非 FK）
	Value     string // 条目实际值
	Label     string // 显示标签（i18n 中文内联）
	Numeric   *int32 // 数值型值（可空）
	SortOrder int32
	Enabled   bool
	Remark    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
