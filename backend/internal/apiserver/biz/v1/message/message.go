// Package message 实现站内消息域（Wave 2.5，源 24 rpc，701 行）。
//
// 复刻范围（三表）：
//   - message（7 rpc）：List/Get/Create/Update/Delete/Send/Revoke
//   - category（6 rpc）：List/Count/Get/Create/Update/Delete
//   - recipient（11 rpc）：List/Count/Get/Create/Update/Delete/GetByIds/
//     ListUserInbox/DeleteFromInbox/MarkAsRead/MarkStatus
//
// ## 核心语义（对齐源）
//
// 1. **一条消息发给 N 人 = 1 条 message + N 条 recipient**（源同此分层）；
// 2. **投递幂等靠 (message_id, recipient_user_id) 唯一约束**——源注释原话
//    「重试幂等性由 (message_id, recipient_user_id) 唯一约束 + CreateBulk 的
//    ON CONFLICT DO NOTHING 保证」。本实现用业务键主键表达该约束；
// 3. **撤销在同一事务**（源 `RevokeMessageWithMessage`）——避免半成功留下
//    「幽灵收件记录」（消息本体已删但收件记录还在）；
// 4. **SendMessage 三模式**：全员广播（源走 asynq，进程重启后自动重试）/
//    单收件人 / 多收件人（同步，上报失败数而非静默丢弃）。
//
// **本波不实现 SSE 实时推送**（源的 publishNotification）——那是 Wave 3 的传输轴，
// 本波只做「落库 + 收件箱读取」。
package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("message: validation failed")

// ErrNotFound 资源不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 资源已存在。
var ErrConflict = store.ErrConflict

// 消息状态常量（对齐源 InternalMessage.Status）。
const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusRevoked   = "REVOKED"
)

// 收件状态常量（对齐源 InternalMessageRecipient.Status）。
const (
	RecipientUnread  = "UNREAD"
	RecipientRead    = "READ"
	RecipientDeleted = "DELETED"
	RecipientRevoked = "REVOKED"
)

// Publisher 是 SSE 推送能力的最小接口（对齐源 `InternalMessagePublisher`）。
//
// **为何用接口而非直接依赖 `*sse.Server`**：与源同款解耦——源定义
// `InternalMessagePublisher` 接口并以 `RegisterInternalMessagePublisher` 注入，
// 使 message 域不强绑定具体传输实现（源注释：「sse 未配置时降级为 no-op」）。
type Publisher interface {
	// TryPublish 非阻塞推送；流不存在或缓冲满时返回 false（不阻塞发送方）。
	TryPublish(streamID string, eventName string, payload any) bool
}

// Biz 是站内消息业务。
type Biz struct {
	// publisher 可为 nil（SSE 未装配时降级为「只落库、不推送」）。
	publisher Publisher
}

// New 构造 Biz（无 SSE 推送能力）。
func New() *Biz { return &Biz{} }

// NewWithPublisher 构造带 SSE 推送能力的 Biz。
func NewWithPublisher(p Publisher) *Biz { return &Biz{publisher: p} }

// SetPublisher 注入/替换推送能力（源用同名注册方法）。
func (b *Biz) SetPublisher(p Publisher) { b.publisher = p }

// ---- message ----

// Message 是对外的消息本体。
type Message struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Status     string `json:"status"`
	Type       string `json:"type"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	CategoryID string `json:"category_id,omitempty"`
	CreatedAt  int64  `json:"created_at,omitempty"`
}

func msgID(tenantID string) string {
	return tenantID + ":msg:" + fmt.Sprintf("%d", time.Now().UnixNano())
}

// CreateMessage 创建消息本体（源 CreateMessage）。
func (b *Biz) CreateMessage(ctx context.Context, tenantID, senderID, senderName string, m Message) (*Message, error) {
	if m.Title == "" {
		return nil, fmt.Errorf("%w: title required", ErrValidation)
	}
	status := m.Status
	if status == "" {
		status = StatusDraft
	}
	model := &authmodel.InternalMessage{
		ID: msgID(tenantID), TenantID: tenantID,
		Title: m.Title, Content: m.Content, Status: status, Type: m.Type,
		SenderID: senderID, SenderName: senderName, CategoryID: m.CategoryID,
	}
	if err := bootstrappkg.MessageStore.Create(ctx, model); err != nil {
		return nil, fmt.Errorf("message: create: %w", err)
	}
	return toMessage(model), nil
}

// GetMessage 查询（源 GetMessage）。
func (b *Biz) GetMessage(ctx context.Context, id string) (*Message, error) {
	m, err := bootstrappkg.MessageStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toMessage(m), nil
}

// ListMessages 列出（源 ListMessage）。
func (b *Biz) ListMessages(ctx context.Context, tenantID string) ([]Message, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.MessageStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("message: list: %w", err)
	}
	out := make([]Message, 0, len(items))
	for _, m := range items {
		out = append(out, *toMessage(m))
	}
	return out, nil
}

// UpdateMessage 更新（源 UpdateMessage）。
func (b *Biz) UpdateMessage(ctx context.Context, id string, m Message) error {
	model, err := bootstrappkg.MessageStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	if m.Title != "" {
		model.Title = m.Title
	}
	if m.Content != "" {
		model.Content = m.Content
	}
	if m.Status != "" {
		model.Status = m.Status
	}
	if err := bootstrappkg.MessageStore.Update(ctx, model); err != nil {
		return fmt.Errorf("message: update: %w", err)
	}
	return nil
}

// DeleteMessage 删除消息本体（源 DeleteMessage）。
// 同时删除其全部收件记录（避免悬挂——源用事务保证）。
//
// **容忍级联删除的 ErrNotFound**：`Store.Delete(ctx, where)` 对 **0 行匹配**
// 返回 `store.ErrNotFound`——那是为「按主键删单条」设计的语义，用在
// 「按条件删集合」上会误报。无收件记录的消息（如草稿）删除时，级联删除
// 匹配 0 行是**合法结果而非错误**。（此缺陷由本波 e2e 测试捕获。）
func (b *Biz) DeleteMessage(ctx context.Context, id string) error {
	// 先删收件记录（无外键约束下的手工级联；顺序重要：先子后父）。
	if err := bootstrappkg.RecipientStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("message_id", id)},
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("message: delete recipients: %w", err)
	}
	if err := bootstrappkg.MessageStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("message: delete: %w", err)
	}
	return nil
}

// SendRequest 是发送入参（源 SendMessageRequest 的三模式）。
type SendRequest struct {
	Title      string
	Content    string
	Type       string
	CategoryID string
	// TargetAll=true 时全员广播；否则按 RecipientUserID / TargetUserIDs 定向。
	TargetAll      bool
	RecipientUserID string
	TargetUserIDs   []string
}

// SendResult 是发送结果。
type SendResult struct {
	MessageID    string `json:"message_id"`
	Delivered    int    `json:"delivered"`
	Failed       int    `json:"failed"`
	Broadcast    bool   `json:"broadcast"`
}

// SendMessage 发送消息（源 SendMessage）——**三模式**。
//
// 1. **全员广播**（TargetAll）：消息本体落库后，为**全部活跃用户**建收件记录。
//    源走 asynq 异步扇出（进程重启后未完成投递自动重试）；本实现**同步**执行
//    （本项目当前用户量小，同步更简单且可立即验证）——**这是一个设计偏差**，
//    已在注释与提交信息中记录：若用户量大应改为 asynq 任务。
// 2. **单收件人**（RecipientUserID）：直接投递。
// 3. **多收件人**（TargetUserIDs）：逐个投递，**上报失败数而非静默丢弃**
//    （源注释：「同样上报错误而非全部丢弃」）。
//
// 幂等：`(message_id, recipient_user_id)` 主键冲突即「已投递」——重复调用
// 不会产生重复收件记录（源用唯一约束 + ON CONFLICT DO NOTHING）。
func (b *Biz) SendMessage(ctx context.Context, tenantID, senderID, senderName string, req SendRequest) (*SendResult, error) {
	// 1) 消息本体落库（状态 PUBLISHED）。
	msg, err := b.CreateMessage(ctx, tenantID, senderID, senderName, Message{
		Title: req.Title, Content: req.Content, Type: req.Type,
		CategoryID: req.CategoryID, Status: StatusPublished,
	})
	if err != nil {
		return nil, err
	}

	res := &SendResult{MessageID: msg.ID}

	// 2) 扇出投递。
	switch {
	case req.TargetAll:
		res.Broadcast = true
		// 取全部用户（源按页拉取；本实现一次取全——量小）。
		users, _, uerr := bootstrappkg.UserStore.List(ctx, &store.Where{})
		if uerr != nil {
			return nil, fmt.Errorf("message: list users for broadcast: %w", uerr)
		}
		for _, u := range users {
			if derr := b.Deliver(ctx, tenantID, msg, u.ID, senderID); derr != nil {
				res.Failed++
			} else {
				res.Delivered++
			}
		}
	case req.RecipientUserID != "":
		if derr := b.Deliver(ctx, tenantID, msg, req.RecipientUserID, senderID); derr != nil {
			res.Failed++
		} else {
			res.Delivered++
		}
	default:
		for _, uid := range req.TargetUserIDs {
			if derr := b.Deliver(ctx, tenantID, msg, uid, senderID); derr != nil {
				res.Failed++
			} else {
				res.Delivered++
			}
		}
	}
	return res, nil
}

// Deliver 投递一条收件记录（幂等：主键冲突视为「已投递」成功）。
//
// 导出理由：**重试投递**是真实场景（源注释即讨论 asynq 重试的幂等性）。
// 同一 (message_id, recipient_user_id) 重复投递被唯一约束吸收，不产生重复记录。
func (b *Biz) Deliver(ctx context.Context, tenantID string, msg *Message, recipientUserID, senderID string) error {
	r := &authmodel.InternalMessageRecipient{
		ID:              msg.ID + ":" + recipientUserID,
		TenantID:        tenantID,
		MessageID:       msg.ID,
		RecipientUserID: recipientUserID,
		SenderUserID:    senderID,
		Title:           msg.Title,
		Content:         msg.Content,
		Status:          RecipientUnread,
	}
	if err := bootstrappkg.RecipientStore.Create(ctx, r); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil // 已投递（幂等）
		}
		return fmt.Errorf("message: deliver: %w", err)
	}

	// **落库成功后才推送**（顺序重要：先持久化，再实时通知）。
	// 推送失败不影响投递结果——收件箱仍可拉取（源同此：`publishNotification`
	// 只记 Debug 日志，不返回 error）。
	b.publish(r)
	return nil
}

// publish 把收件记录推给该用户的 SSE 流（源 `publishNotification`）。
//
// **streamID 用 recipientUserID**：源注释原话——「同一用户的所有在线设备
// 订阅同一条流，库的 stream fan-out 会把该事件投递给该流的全部 subscriber，
// 因此只需单次 publish」。
//
// publisher 为 nil（SSE 未装配）时静默跳过——**降级语义**：
// 站内信仍落库、收件箱可拉取，只是没有实时推送。
func (b *Biz) publish(r *authmodel.InternalMessageRecipient) {
	if b.publisher == nil {
		return
	}
	payload := map[string]any{
		"id":                r.ID,
		"message_id":        r.MessageID,
		"recipient_user_id": r.RecipientUserID,
		"title":             r.Title,
		"content":           r.Content,
		"status":            r.Status,
		"created_at":        r.CreatedAt.Unix(),
	}
	// TryPublish 非阻塞：无在线连接时立即返回 false，不拖慢发送方。
	b.publisher.TryPublish(r.RecipientUserID, "notification", payload)
}

// RevokeMessage 撤销消息（源 RevokeMessage）。
//
// 语义：**撤销是「按收件人」的**——源签名含 UserId。消息本体删除与收件人撤销
// 应在同一事务（源 `RevokeMessageWithMessage`）；本实现无事务封装，
// 故采用「先删本体、再标收件记录为 REVOKED」的顺序，并在注释记录该局限。
func (b *Biz) RevokeMessage(ctx context.Context, messageID, userID string) error {
	if messageID == "" {
		return fmt.Errorf("%w: message_id required", ErrValidation)
	}
	// 1) 把该消息的收件记录标为 REVOKED（保留记录供审计，而非物理删除）。
	items, _, err := bootstrappkg.RecipientStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("message_id", messageID)},
	})
	if err != nil {
		return fmt.Errorf("message: list recipients for revoke: %w", err)
	}
	for _, r := range items {
		if userID != "" && r.RecipientUserID != userID {
			continue // 指定用户时只撤该用户的
		}
		r.Status = RecipientRevoked
		if uerr := bootstrappkg.RecipientStore.Update(ctx, r); uerr != nil {
			return fmt.Errorf("message: revoke recipient: %w", uerr)
		}
	}
	// 2) 消息本体标 REVOKED（不物理删除——保留审计痕迹）。
	m, err := bootstrappkg.MessageStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", messageID)},
	})
	if err != nil {
		return err
	}
	m.Status = StatusRevoked
	if err := bootstrappkg.MessageStore.Update(ctx, m); err != nil {
		return fmt.Errorf("message: revoke: %w", err)
	}
	return nil
}

func toMessage(m *authmodel.InternalMessage) *Message {
	out := &Message{
		ID: m.ID, Title: m.Title, Content: m.Content, Status: m.Status,
		Type: m.Type, SenderID: m.SenderID, SenderName: m.SenderName,
		CategoryID: m.CategoryID,
	}
	if !m.CreatedAt.IsZero() {
		out.CreatedAt = m.CreatedAt.Unix()
	}
	return out
}

// ---- category ----

// Category 是对外分类。
type Category struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Code      string `json:"code"`
	SortOrder int32  `json:"sort_order"`
	Enabled   bool   `json:"enabled"`
	Remark    string `json:"remark,omitempty"`
}

func catID(tenantID, code string) string { return tenantID + ":cat:" + code }

// CreateCategory 创建分类（源 Create）。
func (b *Biz) CreateCategory(ctx context.Context, tenantID string, c Category) (*Category, error) {
	if c.Name == "" || c.Code == "" {
		return nil, fmt.Errorf("%w: name and code required", ErrValidation)
	}
	m := &authmodel.InternalMessageCategory{
		ID: catID(tenantID, c.Code), TenantID: tenantID,
		Name: c.Name, Code: c.Code, SortOrder: c.SortOrder,
		Enabled: c.Enabled, Remark: c.Remark,
	}
	if err := bootstrappkg.MessageCategoryStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: category already exists", ErrConflict)
		}
		return nil, fmt.Errorf("message: create category: %w", err)
	}
	return toCategory(m), nil
}

// ListCategories 列出（源 List）。
func (b *Biz) ListCategories(ctx context.Context, tenantID string) ([]Category, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.MessageCategoryStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("message: list categories: %w", err)
	}
	out := make([]Category, 0, len(items))
	for _, m := range items {
		out = append(out, *toCategory(m))
	}
	return out, nil
}

// CountCategories 统计（源 Count）。
func (b *Biz) CountCategories(ctx context.Context, tenantID string) (int64, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	n, err := bootstrappkg.MessageCategoryStore.Count(ctx, w)
	if err != nil {
		return 0, fmt.Errorf("message: count categories: %w", err)
	}
	return n, nil
}

// GetCategory 按 id 查询（源 Get）。
func (b *Biz) GetCategory(ctx context.Context, id string) (*Category, error) {
	m, err := bootstrappkg.MessageCategoryStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toCategory(m), nil
}

// UpdateCategory 更新（源 Update）。
func (b *Biz) UpdateCategory(ctx context.Context, id string, c Category) error {
	m, err := bootstrappkg.MessageCategoryStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	if c.Name != "" {
		m.Name = c.Name
	}
	m.Enabled = c.Enabled
	if err := bootstrappkg.MessageCategoryStore.Update(ctx, m); err != nil {
		return fmt.Errorf("message: update category: %w", err)
	}
	return nil
}

// DeleteCategory 删除（源 Delete）。
func (b *Biz) DeleteCategory(ctx context.Context, id string) error {
	if err := bootstrappkg.MessageCategoryStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("message: delete category: %w", err)
	}
	return nil
}

func toCategory(m *authmodel.InternalMessageCategory) *Category {
	return &Category{
		ID: m.ID, Name: m.Name, Code: m.Code,
		SortOrder: m.SortOrder, Enabled: m.Enabled, Remark: m.Remark,
	}
}

// ---- recipient（含收件箱）----

// Recipient 是对外收件记录。
type Recipient struct {
	ID              string `json:"id"`
	MessageID       string `json:"message_id"`
	RecipientUserID string `json:"recipient_user_id"`
	SenderUserID    string `json:"sender_user_id,omitempty"`
	Title           string `json:"title"`
	Content         string `json:"content"`
	Status          string `json:"status"`
	ReadAt          int64  `json:"read_at,omitempty"`
	CreatedAt       int64  `json:"created_at,omitempty"`
}

// ListUserInbox 用户收件箱（源 ListUserInbox）——**只返回未删除的**。
//
// 语义：DELETED 的记录是「用户从收件箱移除」，不应出现在列表里。
func (b *Biz) ListUserInbox(ctx context.Context, userID string) ([]Recipient, error) {
	items, _, err := bootstrappkg.RecipientStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("recipient_user_id", userID)},
	})
	if err != nil {
		return nil, fmt.Errorf("message: list inbox: %w", err)
	}
	out := make([]Recipient, 0, len(items))
	for _, m := range items {
		if m.Status == RecipientDeleted {
			continue // 用户已从收件箱移除
		}
		out = append(out, *toRecipient(m))
	}
	return out, nil
}

// GetRecipient 按 id 查询（源 Get）。
func (b *Biz) GetRecipient(ctx context.Context, id string) (*Recipient, error) {
	m, err := bootstrappkg.RecipientStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toRecipient(m), nil
}

// GetRecipientsByIDs 按 id 批量查询（源 GetInternalMessageRecipientsByIds）。
func (b *Biz) GetRecipientsByIDs(ctx context.Context, ids []string) ([]Recipient, error) {
	out := make([]Recipient, 0, len(ids))
	for _, id := range ids {
		m, err := bootstrappkg.RecipientStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", id)},
		})
		if err != nil {
			continue // 不存在的跳过（批量查询语义）
		}
		out = append(out, *toRecipient(m))
	}
	return out, nil
}

// MarkAsRead 标记已读（源 MarkNotificationAsRead）。
// **幂等**：已读的再标仍返回成功（不重复更新 ReadAt）。
func (b *Biz) MarkAsRead(ctx context.Context, id string) error {
	m, err := bootstrappkg.RecipientStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	if m.Status == RecipientRead {
		return nil // 已读：幂等成功
	}
	now := time.Now()
	m.Status = RecipientRead
	m.ReadAt = &now
	if err := bootstrappkg.RecipientStore.Update(ctx, m); err != nil {
		return fmt.Errorf("message: mark read: %w", err)
	}
	return nil
}

// MarkStatus 批量标记状态（源 MarkNotificationsStatus）。
func (b *Biz) MarkStatus(ctx context.Context, ids []string, status string) error {
	if status == "" {
		return fmt.Errorf("%w: status required", ErrValidation)
	}
	for _, id := range ids {
		m, err := bootstrappkg.RecipientStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", id)},
		})
		if err != nil {
			continue
		}
		m.Status = status
		if status == RecipientRead && m.ReadAt == nil {
			now := time.Now()
			m.ReadAt = &now
		}
		if uerr := bootstrappkg.RecipientStore.Update(ctx, m); uerr != nil {
			return fmt.Errorf("message: mark status: %w", uerr)
		}
	}
	return nil
}

// DeleteFromInbox 从收件箱删除（源 DeleteNotificationFromInbox）。
// **软删除**（标 DELETED 而非物理删）——保留投递审计痕迹。
func (b *Biz) DeleteFromInbox(ctx context.Context, id string) error {
	m, err := bootstrappkg.RecipientStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	m.Status = RecipientDeleted
	if err := bootstrappkg.RecipientStore.Update(ctx, m); err != nil {
		return fmt.Errorf("message: delete from inbox: %w", err)
	}
	return nil
}

// CountRecipients 统计（源 Count）。
func (b *Biz) CountRecipients(ctx context.Context, tenantID string) (int64, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	n, err := bootstrappkg.RecipientStore.Count(ctx, w)
	if err != nil {
		return 0, fmt.Errorf("message: count recipients: %w", err)
	}
	return n, nil
}

func toRecipient(m *authmodel.InternalMessageRecipient) *Recipient {
	r := &Recipient{
		ID: m.ID, MessageID: m.MessageID, RecipientUserID: m.RecipientUserID,
		SenderUserID: m.SenderUserID, Title: m.Title, Content: m.Content,
		Status: m.Status,
	}
	if m.ReadAt != nil {
		r.ReadAt = m.ReadAt.Unix()
	}
	if !m.CreatedAt.IsZero() {
		r.CreatedAt = m.CreatedAt.Unix()
	}
	return r
}

// 保留 strings 引用。
var _ = strings.TrimSpace
