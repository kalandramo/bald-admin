package model

import "time"

// MFAMethod 是 MFA 方法枚举。
// 仅 TOTP 已实现（其余方法返回 UNIMPLEMENTED）——故本表当前只承载 TOTP 因子。
type MFAMethod string

const (
	MFAMethodTOTP MFAMethod = "TOTP"
)

// UserMFAFactor 是用户 MFA 因子。
//
// 主键用业务键 `"<tenant>:<user>:<method>"`：以 (tenant,user,method) 唯一约束
// 防重复绑定（绑定前预检 + 唯一索引兜底）。用业务键做主键即可让 Create 冲突
// 自然表达「已绑定」，与 RolePolicy/DictEntry 同范式。
//
// Secret 存**明文 base32**：TOTP secret 必须可读才能算码，无法只存哈希——
// 这是与 token 存储的取舍差异。代价：DB 泄漏即等于 MFA 被绕过（已知设计）。
type UserMFAFactor struct {
	ID         string    `gorm:"primaryKey"` // "<tenant>:<user>:<method>"
	TenantID   string    `gorm:"index"`
	UserID     string    `gorm:"index"`
	Method     MFAMethod // 当前仅 "TOTP"
	Secret     string    // base32 TOTP secret（明文，见结构注释）
	Display    string    // 展示名（如设备名）
	Enabled    bool
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
