package e2e

// t_w3_1_sse_e2e_test.go —— Wave 3.1：SSE 传输轴装配。
//
// ## 装配路径（与 asynq/cron 同为逃生舱，但原因不同）
//
// 契约 `server.sse` 段**存在**（`server.proto:151-157`，3 字段），
// 但 bald **框架不装配它**——`bald/bootstrap/server.go:93` 标注
// `{"sse", false, ...}`，且 `bald/bootstrap/server_sections_test.go:20-40`
// 断言「配了 server.sse 必须报错」。属 **D2 族**（契约段声明但实现没接）。
//
// ## 本测试覆盖
//
//   - 装配成功（NewServer + 各 Option 不报错）；
//   - 订阅端点返回正确的 SSE 协议头（Content-Type: text/event-stream）；
//   - **autoStream 约束**（未开启则首连 500 "Stream not found!"）；
//   - 事件发布 → 订阅方收到（进程内 Stream 层，不依赖 HTTP）。

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	baldsse "github.com/kalandramo/bald/transport/sse"
)

// TestWave3_1_PublishDataRequiresCodec —— **关键约束**：PublishData* 系列
// 依赖 encoding 全局注册表，未注册时 `NewServer` 不报错、PublishData* 才失败。
//
// 与 asynq（Wave 2.1）同族约束。错误信息**明确指引修复方式**（比 D13 好）。
func TestWave3_1_PublishDataRequiresCodec(t *testing.T) {
	// 刻意不注册 codec。
	srv := baldsse.NewServer(":0", baldsse.WithAutoStream(true))
	srv.CreateStream("codec-check")

	err := srv.PublishDataWithEventName(context.Background(), "codec-check", "ev",
		map[string]any{"k": "v"})
	if err == nil {
		t.Fatal("codec 未注册时 PublishData* 应报错（框架行为变化？请核实）")
	}
	if !strings.Contains(err.Error(), "codec is nil") {
		t.Fatalf("错误信息不符: %v", err)
	}
	t.Logf("确认约束: %v", err)
}

// TestWave3_1_SSEProtocolHeaders —— 订阅端点返回正确的 SSE 协议头。
func TestWave3_1_SSEProtocolHeaders(t *testing.T) {
	srv := baldsse.NewServer(":0",
		baldsse.WithPath("/events"),
		baldsse.WithAutoStream(true),
		baldsse.WithAutoReplay(true),
	)

	// 用 httptest 直接驱动 ServeHTTP（不起真实监听）。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?stream=test-stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTP 未在取消后返回")
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q, want text/event-stream", ct)
	}
	t.Logf("SSE 协议头正确: Content-Type=%s", ct)
}

// TestWave3_1_AutoStreamConstraint —— **关键约束**：不开启 autoStream，
// 订阅不存在的流会失败（http.go:79-83 返回 500 "Stream not found!"）。
//
// 这是本波实测发现的未文档化约束——锁住它，防止后人误用。
func TestWave3_1_AutoStreamConstraint(t *testing.T) {
	// 刻意**不开启** autoStream。
	srv := baldsse.NewServer(":0", baldsse.WithPath("/events"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?stream=never-created", nil)
	srv.ServeHTTP(rec, req)

	// 未建流 + autoStream=false → 错误响应（500）。
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("autoStream=false 订阅未知流 status=%d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Stream not found") {
		t.Fatalf("错误信息不符: %s", rec.Body.String())
	}
	t.Logf("确认约束：autoStream=false → %d %q", rec.Code, strings.TrimSpace(rec.Body.String()))
}

// TestWave3_1_PublishToSubscriber —— 发布 → 订阅方收到（真实 HTTP 流）。
//
// 用 httptest.NewServer 而非 ResponseRecorder：后者在 goroutine 并发写时
// 非线程安全，且不模拟真实的流式传输。
func TestWave3_1_PublishToSubscriber(t *testing.T) {
	srv := baldsse.NewServer(":0",
		baldsse.WithPath("/events"),
		baldsse.WithAutoStream(true),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 建立真实 SSE 连接。
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/events?stream=push-test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("建立 SSE 连接: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q", ct)
	}

	// 读响应体的 goroutine。
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	time.Sleep(500 * time.Millisecond) // 等订阅注册到 stream

	// 发布事件。用 TryPublish（直接投递现成 *Event，不经 codec）。
	//
	// **为何不用 PublishDataWithEventName**：它经 `marshalEvent` →
	// `s.codec.Marshal`，而 `NewServer` 在 codec 未注册时
	// `GetCodec("json")` 返回 nil 且**不报错**——直到 PublishData* 才返回
	// `sse: codec is nil (nothing registered); register one via
	// encoding.MustRegister`。本测试**刻意不注册 codec**，以锁住该约束
	// （与 asynq 同族，见 Wave 2.1）。生产装配已注册（sse.go）。
	ok := srv.TryPublish(context.Background(), baldsse.StreamID("push-test"),
		&baldsse.Event{Data: []byte(`{"msg":"hello-sse"}`), Event: []byte("notification")})
	if !ok {
		t.Fatal("TryPublish 返回 false")
	}

	// 等待事件帧到达（含 data 行）。
	deadline := time.After(3 * time.Second)
	var got []string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("连接关闭，已收: %v", got)
			}
			got = append(got, line)
			if strings.Contains(line, "hello-sse") {
				t.Logf("订阅方收到事件帧: %v", got)
				return
			}
		case <-deadline:
			t.Fatalf("未在 3s 内收到事件。已收帧: %v", got)
		}
	}
}

// TestWave3_1_AuthorizeHook —— 授权钩子被调用（签名已核实为
// func(*http.Request, string) error，bald/transport/sse/auth.go:19）。
func TestWave3_1_AuthorizeHook(t *testing.T) {
	called := make(chan string, 1)
	srv := baldsse.NewServer(":0",
		baldsse.WithPath("/events"),
		baldsse.WithAutoStream(true),
		baldsse.WithAuthorizeFunc(func(r *http.Request, token string) error {
			called <- token
			return nil
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?stream=auth-test", nil)
	req.Header.Set("Authorization", "Bearer probe-token")
	ctx, cancel := context.WithCancel(req.Context())
	go srv.ServeHTTP(rec, req.WithContext(ctx))

	select {
	case tok := <-called:
		if tok != "probe-token" {
			t.Fatalf("授权钩子收到 token=%q, want probe-token", tok)
		}
		t.Logf("授权钩子被调用，token=%s（默认 TokenExtractor 从 Authorization 头提取）", tok)
	case <-time.After(2 * time.Second):
		t.Fatal("授权钩子未被调用")
	}
	cancel()
	_ = bufio.NewReader
}
