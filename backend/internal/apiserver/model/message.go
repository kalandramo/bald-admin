package model

import "time"

// InternalMessage 站内消息本体。
//
// 语义分层：本表是**消息本体**（谁发的、标题内容、状态），
// 与 InternalMessageRecipient（**每个收件人一份投递记录**）是 1:N 关系。
// 设计如此——「一条消息发给 N 人」= 1 条 message + N 条 recipient。
type InternalMessage struct {
	ID       string `gorm:"primaryKey"` // 消息 ID（业务键）
	TenantID string `gorm:"index"`
	Title    string
	Content  string
	// Status 消息状态：DRAFT / PUBLISHED / REVOKED。
	Status string
	// Type 消息类型：NOTIFICATION / ANNOUNCEMENT / …
	Type       string
	SenderID   string `gorm:"index"`
	SenderName string
	CategoryID string `gorm:"index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// InternalMessageCategory 消息分类。
type InternalMessageCategory struct {
	ID        string `gorm:"primaryKey"` // "<tenant>:<code>"
	TenantID  string `gorm:"index"`
	Name      string
	Code      string `gorm:"index"`
	SortOrder int32
	Enabled   bool
	Remark    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InternalMessageRecipient 消息收件记录——**每收件人一份**。
//
// 主键用业务键 `"<message_id>:<recipient_user_id>"`：幂等性建立在这个
// 唯一约束上（重试幂等由 (message_id, recipient_user_id) 唯一约束 +
// CreateBulk 的 ON CONFLICT DO NOTHING 保证）。
// 业务键主键即该约束的等价实现——重复投递自然冲突。
type InternalMessageRecipient struct {
	ID              string `gorm:"primaryKey"` // "<message_id>:<recipient_user_id>"
	TenantID        string `gorm:"index"`
	MessageID       string `gorm:"index"`
	RecipientUserID string `gorm:"index"`
	SenderUserID    string
	Title           string
	Content         string
	// Status 收件状态：UNREAD / READ / DELETED / REVOKED。
	Status    string `gorm:"index"`
	ReadAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}
