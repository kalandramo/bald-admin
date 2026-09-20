package model

import "time"

// Language 语言条目（Wave 5.3，自 go-wind-admin dict 域 language 精简移植）。
//
// ## 平台级数据（无 TenantID）——重要判断
//
// 源 `language.proto` **无 tenant_id 字段**（实测 `grep tenant` 零命中），
// 即语言是**平台级**共享数据（所有租户共用同一份语言列表），与同目录的
// DictType/DictEntry（租户级，带 TenantID + P8 自动隔离）**不同**。
// 故本模型不带 TenantID，Store 查询不走租户过滤——这是对齐源语义，
// 不是遗漏。
//
// ## 主键
//
// ID 即标准语言代码（源 language_code，如 "zh-CN"）——业务键主键，
// 同 DictType（type_code 作主键）范式。语言代码天然唯一且可读。
type Language struct {
	ID           string `gorm:"primaryKey"` // 标准语言代码（如 "zh-CN"）
	LanguageName string // 语言名称（如 "中文（简体）"）
	NativeName   string // 本地语言名称（如 "简体中文"）
	IsDefault    bool   // 是否默认语言
	IsEnabled    bool   // 是否启用
	SortOrder    int32  // 排序（值越小越靠前）
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
