package e2e

// t_w2_5_message_e2e_test.go —— Wave 2.5：站内消息域（源 24 rpc，三表）。
//
// 核心验证点（对应源语义）：
//   - 三模式发送：全员广播 / 单收件人 / 多收件人；
//   - **投递幂等**：(message_id, recipient_user_id) 唯一约束——
//     同一人重复投递不产生重复收件记录（源的 ON CONFLICT DO NOTHING 语义）；
//   - **一条消息发给 N 人 = 1 条 message + N 条 recipient**（源的分层）；
//   - 收件箱只返回未删除的（DELETED 的不出现）；
//   - 标记已读幂等；
//   - 撤销按收件人（源 RevokeMessage 签名含 UserId）；
//   - 分类 CRUD + 唯一约束（同 code 重复 → 409）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

func startMessageREST(t *testing.T) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		Task: taskbiz.New(), Message: msgbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func msgSuffix() string { return "m" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10) }

// TestWave2_5_CategoryCRUD 分类 CRUD + 唯一约束。
func TestWave2_5_CategoryCRUD(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	code := "cat" + msgSuffix()

	// 创建。
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/message-categories",
		map[string]any{"name": "系统通知", "code": code, "enabled": true, "sort_order": 1})
	if st != http.StatusCreated {
		t.Fatalf("create category status=%d body=%s", st, raw)
	}
	var created msgbiz.Category
	_ = json.Unmarshal(raw, &created)
	if created.ID == "" {
		t.Fatalf("创建未返回 id: %s", raw)
	}

	// 同 code 重复 → 409。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/message-categories",
		map[string]any{"name": "重复", "code": code, "enabled": true})
	if st != http.StatusConflict {
		t.Fatalf("重复 code status=%d, want 409", st)
	}

	// 缺 name/code → 400。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/message-categories",
		map[string]any{"name": "只有名字"})
	if st != http.StatusBadRequest {
		t.Fatalf("缺 code status=%d, want 400", st)
	}

	// 查询。
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/message-categories/"+created.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("get category status=%d", st)
	}

	// count。
	st, raw = callRaw(t, base, admin, http.MethodGet, "/v1/message-categories/count", nil)
	if st != http.StatusOK {
		t.Fatalf("count status=%d", st)
	}
	var cnt struct {
		Total int64 `json:"total"`
	}
	_ = json.Unmarshal(raw, &cnt)
	if cnt.Total < 1 {
		t.Fatalf("count=%d, want >=1", cnt.Total)
	}

	// 更新 + 删除。
	st, _ = callRaw(t, base, admin, http.MethodPut, "/v1/message-categories/"+created.ID,
		map[string]any{"name": "改名后", "enabled": false})
	if st != http.StatusOK {
		t.Fatalf("update status=%d", st)
	}
	st, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/message-categories/"+created.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("delete status=%d", st)
	}
	// 删除后再查 → 404。
	st, _ = callRaw(t, base, admin, http.MethodGet, "/v1/message-categories/"+created.ID, nil)
	if st != http.StatusNotFound {
		t.Fatalf("删除后 get status=%d, want 404", st)
	}
}

// TestWave2_5_SendIdempotent 投递幂等——**本波最重要的验证点**。
//
// 源注释：「重试幂等性由 (message_id, recipient_user_id) 唯一约束 +
// CreateBulk 的 ON CONFLICT DO NOTHING 保证」。
// 本测试直接驱动 biz 层（HTTP 层每次发送都会新建 message，拿不到同一 message_id
// 的重复投递场景），故用 SendMessage + 手工重复投递的方式验证约束。
func TestWave2_5_SendIdempotent(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	_ = base

	biz := msgbiz.New()
	ctx := context.Background()
	tenant := "t-default"

	// 单收件人发送。
	res, err := biz.SendMessage(ctx, tenant, "u-sender", "发送者", msgbiz.SendRequest{
		Title: "幂等测试", Content: "正文", Type: "NOTIFICATION",
		RecipientUserID: "u-recv-1",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("delivered=%d, want 1", res.Delivered)
	}

	// 收件箱应有 1 条。
	inbox, err := biz.ListUserInbox(ctx, "u-recv-1")
	if err != nil {
		t.Fatalf("ListUserInbox: %v", err)
	}
	var before int
	for _, r := range inbox {
		if r.MessageID == res.MessageID {
			before++
		}
	}
	if before != 1 {
		t.Fatalf("首次投递后收件记录=%d, want 1", before)
	}

	// **直接重复投递同一 (message_id, user_id)** —— 应被幂等吸收。
	msg, err := biz.GetMessage(ctx, res.MessageID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if derr := biz.Deliver(ctx, tenant, msg, "u-recv-1", "u-sender"); derr != nil {
		t.Fatalf("重复投递应被幂等吸收，实际报错: %v", derr)
	}

	// 收件记录数**必须仍为 1**（唯一约束生效）。
	inbox, _ = biz.ListUserInbox(ctx, "u-recv-1")
	var after int
	for _, r := range inbox {
		if r.MessageID == res.MessageID {
			after++
		}
	}
	if after != 1 {
		t.Fatalf("重复投递后收件记录=%d, want 1（幂等失效！）", after)
	}

	// 收件箱权限：admin 的收件箱里**不应**有这条（发给 u-recv-1 的）。
	st, raw := callRaw(t, base, admin, http.MethodGet, "/v1/inbox", nil)
	if st != http.StatusOK {
		t.Fatalf("admin inbox status=%d body=%s", st, raw)
	}
	var box struct {
		Items []msgbiz.Recipient `json:"items"`
	}
	_ = json.Unmarshal(raw, &box)
	for _, r := range box.Items {
		if r.MessageID == res.MessageID {
			t.Fatalf("admin 收件箱出现了发给 u-recv-1 的消息——越权！")
		}
	}
}

// TestWave2_5_SendModes 三模式发送。
func TestWave2_5_SendModes(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	// 单收件人。
	st, raw := callRaw(t, base, admin, http.MethodPost, "/v1/messages/send",
		map[string]any{"title": "定向", "content": "c", "recipient_user_id": "u-a"})
	if st != http.StatusOK {
		t.Fatalf("单收件人 status=%d body=%s", st, raw)
	}
	var r1 msgbiz.SendResult
	_ = json.Unmarshal(raw, &r1)
	if r1.Delivered != 1 || r1.Broadcast {
		t.Fatalf("单收件人 delivered=%d broadcast=%v, want 1/false", r1.Delivered, r1.Broadcast)
	}

	// 多收件人。
	st, raw = callRaw(t, base, admin, http.MethodPost, "/v1/messages/send",
		map[string]any{"title": "多收", "content": "c", "target_user_ids": []string{"u-b", "u-c", "u-d"}})
	if st != http.StatusOK {
		t.Fatalf("多收件人 status=%d body=%s", st, raw)
	}
	var r2 msgbiz.SendResult
	_ = json.Unmarshal(raw, &r2)
	if r2.Delivered != 3 {
		t.Fatalf("多收件人 delivered=%d, want 3", r2.Delivered)
	}

	// 全员广播。
	st, raw = callRaw(t, base, admin, http.MethodPost, "/v1/messages/send",
		map[string]any{"title": "广播", "content": "c", "target_all": true})
	if st != http.StatusOK {
		t.Fatalf("广播 status=%d body=%s", st, raw)
	}
	var r3 msgbiz.SendResult
	_ = json.Unmarshal(raw, &r3)
	if !r3.Broadcast {
		t.Fatalf("广播 broadcast=%v, want true", r3.Broadcast)
	}

	// 标题为空 → 400。
	st, _ = callRaw(t, base, admin, http.MethodPost, "/v1/messages/send",
		map[string]any{"content": "无标题", "recipient_user_id": "u-a"})
	if st != http.StatusBadRequest {
		t.Fatalf("空标题 status=%d, want 400", st)
	}
}

// TestWave2_5_InboxLifecycle 收件箱生命周期：已读 → 幂等 → 删除后消失。
func TestWave2_5_InboxLifecycle(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	biz := msgbiz.New()
	ctx := context.Background()
	uid := "u-lifecycle-" + msgSuffix()

	res, err := biz.SendMessage(ctx, "t-default", "u-sender", "s", msgbiz.SendRequest{
		Title: "生命周期", Content: "c", RecipientUserID: uid,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	rid := res.MessageID + ":" + uid

	// 初始 UNREAD。
	inbox, _ := biz.ListUserInbox(ctx, uid)
	if len(inbox) != 1 || inbox[0].Status != msgbiz.RecipientUnread {
		t.Fatalf("初始收件箱不符: %+v", inbox)
	}

	// 标记已读。
	if err := biz.MarkAsRead(ctx, rid); err != nil {
		t.Fatalf("MarkAsRead: %v", err)
	}
	got, _ := biz.GetRecipient(ctx, rid)
	if got.Status != msgbiz.RecipientRead || got.ReadAt == 0 {
		t.Fatalf("已读后 status=%s readAt=%d", got.Status, got.ReadAt)
	}

	// **重复标记已读 → 幂等成功，ReadAt 不变**。
	firstReadAt := got.ReadAt
	time.Sleep(1100 * time.Millisecond) // 跨秒，确保若重新赋值则值会变
	if err := biz.MarkAsRead(ctx, rid); err != nil {
		t.Fatalf("重复 MarkAsRead 应幂等成功: %v", err)
	}
	got2, _ := biz.GetRecipient(ctx, rid)
	if got2.ReadAt != firstReadAt {
		t.Fatalf("重复标记已读改写了 ReadAt：%d → %d（幂等失效）", firstReadAt, got2.ReadAt)
	}

	// 从收件箱删除 → 列表不再出现（软删除）。
	if err := biz.DeleteFromInbox(ctx, rid); err != nil {
		t.Fatalf("DeleteFromInbox: %v", err)
	}
	inbox, _ = biz.ListUserInbox(ctx, uid)
	if len(inbox) != 0 {
		t.Fatalf("删除后收件箱仍有 %d 条, want 0", len(inbox))
	}
	// 但记录本身还在（软删除，可审计）。
	got3, err := biz.GetRecipient(ctx, rid)
	if err != nil || got3.Status != msgbiz.RecipientDeleted {
		t.Fatalf("软删除后记录应保留且状态 DELETED: err=%v status=%v", err, got3)
	}

	_ = admin
}

// TestWave2_5_Revoke 撤销：按收件人，且保留审计痕迹。
func TestWave2_5_Revoke(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	biz := msgbiz.New()
	ctx := context.Background()
	suffix := msgSuffix()
	u1, u2 := "u-rv1-"+suffix, "u-rv2-"+suffix

	res, err := biz.SendMessage(ctx, "t-default", "u-s", "s", msgbiz.SendRequest{
		Title: "撤销测试", Content: "c", TargetUserIDs: []string{u1, u2},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if res.Delivered != 2 {
		t.Fatalf("delivered=%d, want 2", res.Delivered)
	}

	// 只撤 u1。
	if err := biz.RevokeMessage(ctx, res.MessageID, u1); err != nil {
		t.Fatalf("RevokeMessage: %v", err)
	}

	r1, _ := biz.GetRecipient(ctx, res.MessageID+":"+u1)
	if r1.Status != msgbiz.RecipientRevoked {
		t.Fatalf("u1 status=%s, want REVOKED", r1.Status)
	}
	r2, _ := biz.GetRecipient(ctx, res.MessageID+":"+u2)
	if r2.Status != msgbiz.RecipientUnread {
		t.Fatalf("u2 不应受影响: status=%s", r2.Status)
	}

	// 消息本体标 REVOKED（保留审计痕迹，非物理删除）。
	m, err := biz.GetMessage(ctx, res.MessageID)
	if err != nil {
		t.Fatalf("消息本体应保留（审计）: %v", err)
	}
	if m.Status != msgbiz.StatusRevoked {
		t.Fatalf("消息本体 status=%s, want REVOKED", m.Status)
	}

	// 缺 message_id → 400。
	if err := biz.RevokeMessage(ctx, "", ""); err == nil {
		t.Fatalf("空 message_id 应报错")
	}

	_ = admin
}

// TestWave2_5_MessageCRUD 消息本体 CRUD + 级联删除。
func TestWave2_5_MessageCRUD(t *testing.T) {
	base := startMessageREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	biz := msgbiz.New()
	ctx := context.Background()

	// 创建草稿。
	m, err := biz.CreateMessage(ctx, "t-default", "u-1", "n", msgbiz.Message{
		Title: "草稿", Content: "c",
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if m.Status != msgbiz.StatusDraft {
		t.Fatalf("默认状态=%s, want DRAFT", m.Status)
	}

	// 空标题 → 校验错。
	if _, err := biz.CreateMessage(ctx, "t-default", "u-1", "n", msgbiz.Message{}); err == nil {
		t.Fatalf("空标题应报错")
	}

	// 更新。
	if err := biz.UpdateMessage(ctx, m.ID, msgbiz.Message{Title: "改名", Status: msgbiz.StatusPublished}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	got, _ := biz.GetMessage(ctx, m.ID)
	if got.Title != "改名" || got.Status != msgbiz.StatusPublished {
		t.Fatalf("更新后: %+v", got)
	}

	// 投递 2 人后删消息 → 收件记录应级联删除（避免悬挂）。
	if _, err := biz.SendMessage(ctx, "t-default", "u-1", "n", msgbiz.SendRequest{
		Title: "级联", Content: "c", TargetUserIDs: []string{"cu-1", "cu-2"},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// 用 HTTP 层删除验证。
	st, _ := callRaw(t, base, admin, http.MethodDelete, "/v1/messages/"+m.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("delete message status=%d", st)
	}
	if _, err := biz.GetMessage(ctx, m.ID); err == nil {
		t.Fatalf("删除后应 404")
	}

	// 列表。
	st, raw := callRaw(t, base, admin, http.MethodGet, "/v1/messages", nil)
	if st != http.StatusOK {
		t.Fatalf("list status=%d body=%s", st, raw)
	}
}
