package model

import "time"

// MFAMethod 是 MFA 方法枚举（对齐源 mfa.proto 的 MFAMethod）。
// 仅 TOTP 在本轮实现（源 mfa_service.go 文件头注释载明「本轮仅落地 TOTP」，
// 其余方法返回 UNIMPLEMENTED）——故本表当前只承载 TOTP 因子。
type MFAMethod string

const (
	MFAMethodTOTP MFAMethod = "TOTP"
)

// UserMFAFactor 是用户 MFA 因子（Wave 1.5，自源 usermfafactor 精简移植）。
//
// 主键用业务键 `"<tenant>:<user>:<method>"`：源以 (tenant,user,method) 唯一约束
// 防重复绑定（`mfa_service.go:140-148` 的 HasEnabledTotp 预检 + 唯一索引兜底）。
// 用业务键做主键即可让 Create 冲突自然表达「已绑定」，与 RolePolicy/DictEntry 同范式。
//
// Secret 存**明文 base32**（源同此：`CreateTotpFactor` 落库 secret 后由 repo 解密）。
// 这是与 token 存储的取舍差异——TOTP secret 必须可读才能算码，无法只存哈希。
// 代价记录在缺陷报告：DB 泄漏即等于 MFA 被绕过（源同样如此，属已知设计）。
type UserMFAFactor struct {
	ID        string    `gorm:"primaryKey"` // "<tenant>:<user>:<method>"
	TenantID  string    `gorm:"index"`
	UserID    string    `gorm:"index"`
	Method    MFAMethod // 当前仅 "TOTP"
	Secret    string    // base32 TOTP secret（明文，见结构注释）
	Display   string    // 展示名（源 display，如设备名）
	Enabled   bool
	LastUsedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}
