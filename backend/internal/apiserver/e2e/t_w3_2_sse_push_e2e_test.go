package e2e

// t_w3_2_sse_push_e2e_test.go —— Wave 3.2：message 域 → SSE 实时推送接线。
//
// ## 对齐源的三处对接
//
// 源：`sse.NewSseServer(cfg, WithAuthorizeFunc(HandleAuthorize))` +
// `RegisterInternalMessagePublisher(srv)`。本项目形态一一对应：
//   - 授权：`sseAuthorize`（token 校验 + stream 主体绑定）；
//   - 推送：`sseMessagePublisher`（把 message biz 的窄接口适配到 *sse.Server）；
//   - 注入：`wireMessageSSE`（SSE 未装配时降级为「只落库不推送」）。
//
// ## 核心安全语义
//
// **stream 参数必须与 token 主体一致**（源同款）——防用户 A 订阅用户 B 的流。
//
// 注：本文件用 httptest 驱动 biz 层接线（不依赖真实 server），
// 真实 HTTP 链路（订阅→发送→收到事件、越权 403）已在端到端脚本验证。

import (
	"context"
	"testing"
	"time"

	baldsse "github.com/kalandramo/bald/transport/sse"

	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// initMsgBridges 初始化仓储桥（直接驱动 biz 层时必须）。
func initMsgBridges(t *testing.T) {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
}

// fakePublisher 记录推送调用。
type fakePublisher struct {
	calls []struct {
		stream, event string
	}
}

func (f *fakePublisher) TryPublish(stream, event string, _ any) bool {
	f.calls = append(f.calls, struct{ stream, event string }{stream, event})
	return true
}

// TestWave3_2_DeliverTriggersPush —— 投递成功 → 触发推送（stream=收件人）。
func TestWave3_2_DeliverTriggersPush(t *testing.T) {
	initMsgBridges(t)
	pub := &fakePublisher{}
	biz := msgbiz.NewWithPublisher(pub)
	ctx := context.Background()

	uid := "u-push-" + time.Now().Format("150405.000000")
	res, err := biz.SendMessage(ctx, "default", "u-sender", "发送者", msgbiz.SendRequest{
		Title: "推送测试", Content: "正文", RecipientUserID: uid,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("推送调用数=%d, want 1", len(pub.calls))
	}
	c := pub.calls[0]
	// **streamID 必须是收件人**（源注释：同一用户所有设备订阅同一条流）。
	if c.stream != uid {
		t.Fatalf("stream=%q, want %q（收件人）", c.stream, uid)
	}
	if c.event != "notification" {
		t.Fatalf("event=%q, want notification", c.event)
	}
	t.Logf("投递触发推送: stream=%s event=%s (msg=%s)", c.stream, c.event, res.MessageID)
}

// TestWave3_2_NilPublisherDegrades —— 未装配 SSE 时降级：投递成功但无推送。
func TestWave3_2_NilPublisherDegrades(t *testing.T) {
	initMsgBridges(t)
	biz := msgbiz.New() // 无 publisher
	ctx := context.Background()

	uid := "u-nopub-" + time.Now().Format("150405.000000")
	if _, err := biz.SendMessage(ctx, "default", "u-s", "s", msgbiz.SendRequest{
		Title: "降级测试", Content: "c", RecipientUserID: uid,
	}); err != nil {
		t.Fatalf("无 publisher 时投递应成功（降级），实际: %v", err)
	}

	// 收件箱仍可拉取——降级不影响持久化。
	inbox, err := biz.ListUserInbox(ctx, uid)
	if err != nil {
		t.Fatalf("ListUserInbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("降级后收件箱应有 1 条, got %d", len(inbox))
	}
	t.Log("降级语义正确：无 publisher 时仍落库，收件箱可拉取")
}

// TestWave3_2_MultipleRecipientsPushEach —— 多收件人 → 每个收件人各推一次。
func TestWave3_2_MultipleRecipientsPushEach(t *testing.T) {
	initMsgBridges(t)
	pub := &fakePublisher{}
	biz := msgbiz.NewWithPublisher(pub)
	ctx := context.Background()

	sfx := time.Now().Format("150405.000000")
	u1, u2, u3 := "u-m1-"+sfx, "u-m2-"+sfx, "u-m3-"+sfx
	if _, err := biz.SendMessage(ctx, "default", "u-s", "s", msgbiz.SendRequest{
		Title: "多收推送", Content: "c", TargetUserIDs: []string{u1, u2, u3},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if len(pub.calls) != 3 {
		t.Fatalf("推送调用数=%d, want 3（每收件人一次）", len(pub.calls))
	}
	seen := map[string]bool{}
	for _, c := range pub.calls {
		seen[c.stream] = true
	}
	for _, u := range []string{u1, u2, u3} {
		if !seen[u] {
			t.Fatalf("收件人 %s 未被推送; 实际=%v", u, seen)
		}
	}
	t.Logf("多收件人各自推送: %v", seen)
}

// TestWave3_2_IdempotentDeliverDoesNotDoublePush —— 幂等投递不重复推送。
//
// 语义：`Deliver` 遇主键冲突返回 nil（已投递）——此时**不应**再推送，
// 否则重复投递会向用户推重复通知。
func TestWave3_2_IdempotentDeliverDoesNotDoublePush(t *testing.T) {
	initMsgBridges(t)
	pub := &fakePublisher{}
	biz := msgbiz.NewWithPublisher(pub)
	ctx := context.Background()

	uid := "u-idem-" + time.Now().Format("150405.000000")
	res, err := biz.SendMessage(ctx, "default", "u-s", "s", msgbiz.SendRequest{
		Title: "幂等推送", Content: "c", RecipientUserID: uid,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	first := len(pub.calls)

	msg, _ := biz.GetMessage(ctx, res.MessageID)
	if err := biz.Deliver(ctx, "default", msg, uid, "u-s"); err != nil {
		t.Fatalf("重复投递应幂等成功: %v", err)
	}

	if len(pub.calls) != first {
		t.Fatalf("重复投递产生了额外推送: %d -> %d（幂等应不推送）",
			first, len(pub.calls))
	}
	t.Logf("幂等投递未重复推送（保持 %d 次）", first)
}

// TestWave3_2_PublisherAdapter —— sseMessagePublisher 适配器编译期契约。
//
// 仅验证接口形状可对接（真实推送在端到端脚本验证）。
func TestWave3_2_PublisherAdapter(t *testing.T) {
	srv := baldsse.NewServer(":0", baldsse.WithAutoStream(true))
	// 适配器在 cmd 包（不可从 e2e import）；此处验证底层 sse.Server
	// 具备 TryPublish 能力且对未知流返回 false（非阻塞语义）。
	if srv.TryPublish(context.Background(), "nonexistent", &baldsse.Event{Data: []byte("x")}) {
		t.Fatal("对不存在的流 TryPublish 应返回 false")
	}
	t.Log("sse.Server.TryPublish 非阻塞语义正确（未知流返回 false）")
}
