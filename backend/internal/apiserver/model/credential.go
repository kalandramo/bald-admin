package model

import "time"

// UserCredential 用户凭证（Wave 1.6，自源 user_credential.proto 精简移植）。
//
// 源设计为「一个用户可有多种身份类型的凭证」（USERNAME/EMAIL/PHONE/OAuth/SSO/
// API Key/设备/生物识别…，见 proto 的 IdentityType 枚举 12 项），本表承载同一模型。
//
// 主键用业务键 `"<identity_type>:<identifier>"`：源以 (user_id, identity_type,
// identifier) 唯一约束保证「同一身份类型下标识不重复」，业务键主键即该约束的
// 等价实现（Create 冲突自然表达重复），与 DictEntry/RolePolicy 同范式。
type UserCredential struct {
	ID           string `gorm:"primaryKey"` // "<identity_type>:<identifier>"
	TenantID     string `gorm:"index"`
	UserID       string `gorm:"index"`
	IdentityType string // USERNAME / EMAIL / PHONE / SOCIAL_OAUTH / ...
	// Identifier 是身份标识（用户名/邮箱/手机号/第三方 openid 等）。
	Identifier string `gorm:"index"`
	// CredentialType 是凭证类型（PASSWORD_HASH / OTP / TOTP / OAUTH_TOKEN ...）。
	CredentialType string
	// Credential 是凭证本体（密码哈希 / 密钥密文 / 令牌等）。
	// **注意**：密码类存 bcrypt 哈希；密钥类应加密存储（源由 repo 层负责，
	// 本实现记录该责任点，见 credential.go 注释）。
	Credential string
	// Status 是凭证状态（ENABLED/DISABLED/EXPIRED/UNVERIFIED/REMOVED/BLOCKED/TEMPORARY）。
	Status string
	// Extra 承载类型特有属性（JSON 文本，源用 protobuf Any/Struct）。
	Extra     string
	ExpiresAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LoginPolicy 登录策略（Wave 1.6，自源 login_policy.proto 精简移植）。
//
// 语义：限制**哪些来源**可以登录（黑/白名单 × IP/MAC/地区/时间/设备）。
// 源的关键设计：白名单若配错格式会静默不命中 → **全员被锁**，
// 故 Create/Update 必须做值格式校验（见 internal/security/loginpolicy）。
type LoginPolicy struct {
	ID       string `gorm:"primaryKey"` // "<tenant>:<type>:<method>:<value>"
	TenantID string `gorm:"index"`
	TargetID string `gorm:"index"` // 目标用户 ID（空 = 租户级策略）
	Type     string // BLACKLIST / WHITELIST
	Method   string // IP / MAC / REGION / TIME / DEVICE
	Value    string // 限制值（IP/CIDR、MAC、地区码、HH:MM-HH:MM、设备 ID）
	Reason   string
	CreatedBy string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
