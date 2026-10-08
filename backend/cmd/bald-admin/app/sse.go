// sse.go —— SSE 传输轴装配 + message 域推送接线。
//
// ## 装配路径：运行期构造的**逃生舱**（`WithExtraServerFunc`，非 WithExtraServers）
//
// 契约 `server.sse` 段**存在**（`server.proto`：addr/path/tls），但本仓**未提供
// sse 的 contract provider**——`bald/transport/` 下现有 contract 子包只有
// `asynq`/`cron`/`gateway` 三个；`bootstrap/server.go` 的 `serverSections`
// 中 `sse` 为 `implemented=false`。故配了 `server.sse` 段而**未**注册 provider
// 会 fail-fast（`validateServerSections`）。
//
// **为什么不为 sse 补 contract（与 asynq/cron/gateway 不同）**：
// SSE 的接线本质是**函数注入**——授权钩子须绑业务 `authenticator`
// （`baldsse.WithAuthorizeFunc(...)`），订阅回调与推送发布同理；而契约段是
// **声明式配置字段**，表达不了函数。框架对同类需求（如审计后端注入
// `AuditProvider`）同样是函数注入而非契约段。
//
// **为何用 `WithExtraServerFunc` 而非 `WithExtraServers`**：工厂在 **Run 期
// server 构造点**（业务 beforeStart 之后）被调用，彼时 `InitBridges` 已建好
// authenticator —— 故可**运行期构造**并当场完成 biz 接线；`WithExtraServers`
// 收已构造实例，无法满足该时序。
//
// ## 与源项目的对照（3.2 接线）
//
// 源：`sse.NewSseServer(cfg.Server.Sse, WithSubscriberFunction(...),
// WithAuthorizeFunc(internalMessageService.HandleAuthorize))`，
// 然后 `internalMessageService.RegisterInternalMessagePublisher(srv)`。
// bald 的 `transport/sse` 提供**同名同形的 Option**（`options.go`），
// 故三处对接（授权 / 订阅回调 / 推送发布）形态可一一对应。
package app

import (
	"context"
	stdjson "encoding/json"
	"net/http"

	"github.com/kalandramo/bald/encoding"
	baldjson "github.com/kalandramo/bald/encoding/json"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/transport"
	baldsse "github.com/kalandramo/bald/transport/sse"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
)

// ensureJSONCodec 确保 json codec 已注册（SSE 的 PublishData* 依赖它）。
//
// **与 asynq 同族约束**（Wave 2.1 已踩过）：bald 的 `encoding` 是全局注册表，
// `NewServer` 内 `GetCodec("json")` 在未注册时返回 nil 且**不报错**，
// 直到 `PublishData*` 才返回
// `sse: codec is nil (nothing registered); register one via encoding.MustRegister`。
//
// **注意 `MustRegister` 不幂等**——对已注册的 name 会 **panic**
// （`bald/encoding@v0.1.0/encoding.go:42-44`）。asynq.go 已注册 json，
// 故此处先 `GetCodec` 探测再决定是否注册（**幂等包装**）。
// 此约束由本次端到端启动实测捕获（首次实现直接 init+MustRegister → panic）。
func ensureJSONCodec() {
	if encoding.GetCodec("json") == nil {
		encoding.MustRegister(baldjson.New())
	}
}

// sseConfig 是 SSE 装配配置（逃生舱下自读环境变量）。
type sseConfig struct {
	Addr string
	Path string
}

// loadSSEConfig 返回 SSE 装配配置（W2 收敛：取自 ServerOptions.SSE，
// 其默认值来源为 env BALD_ADMIN_SSE_ADDR / BALD_ADMIN_SSE_PATH）。
func loadSSEConfig(opts *options.ServerOptions) sseConfig {
	return sseConfig{Addr: opts.SSE.Addr, Path: opts.SSE.Path}
}

// buildSSEServer 构造 SSE 服务器（Wave 3.1/3.2）。
//
// 返回 (nil, nil) 表示「未配置 SSE」——与 asynq 的「无 Redis 不启用」同款
// 可选语义（判据不同：asynq 看依赖可达性，SSE 看配置是否显式声明）。
func buildSSEServer(ctx context.Context, cfg sseConfig, authn authn.Authenticator) (transport.Server, error) {
	if cfg.Addr == "" {
		return nil, nil // 未配置 → 不启用
	}
	ensureJSONCodec()
	if cfg.Path == "" {
		cfg.Path = "/events" // 与契约默认值一致（server.proto:154）
	}

	opts := []baldsse.Option{
		baldsse.WithPath(cfg.Path),
		// **关键约束（探针实测）**：`autoStream` 默认 false，而 `ServeHTTP`
		// 在「流不存在 && autoStream=false」时返回 500 "Stream not found!"
		// （`http.go:79-83`）。开启后首次订阅自动建流。
		baldsse.WithAutoStream(true),
		// 断线重连补发（SSE 协议标准语义，源同样具备）。
		baldsse.WithAutoReplay(true),
		// 授权钩子：校验 token 且**绑定 stream 到 token 所属用户**
		// （源 `HandleAuthorize` 同款，防跨用户订阅）。
		baldsse.WithAuthorizeFunc(sseAuthorize(authn)),
	}

	srv := baldsse.NewServer(cfg.Addr, opts...)
	log.Info(ctx, "sse server built", "addr", cfg.Addr, "path", cfg.Path)
	return srv, nil
}

// sseAuthorize 返回 SSE 订阅授权钩子（源 `HandleAuthorize` 的对应物）。
//
// 签名已核实：`func(*http.Request, string) error`（`bald/transport/sse/auth.go:19`）。
//
// **核心安全语义（对齐源）**：
//  1. 校验 token（含过期）——无效 → 401；
//  2. token 的 Subject 必须与 stream 参数**一致**——否则 403。
//     这防止用户 A 订阅用户 B 的消息流（越权窃听）。
//     源的做法：`if streamUserId != tokenUserId { return ErrorForbidden(
//     "stream user mismatch") }`。
//
// 返回 `sse.ErrForbidden` 产生 403，其他 error 产生 401（框架契约，见 auth.go:18）。
func sseAuthorize(authenticator authn.Authenticator) baldsse.AuthorizeFunc {
	return func(r *http.Request, token string) error {
		// 1) token 必须存在且有效。
		if token == "" {
			return errSSEUnauthorized("missing token")
		}
		claims, err := authenticator.AuthenticateToken(token)
		if err != nil {
			return errSSEUnauthorized("invalid token: " + err.Error())
		}

		// 2) stream 参数必须与 token 主体一致（防跨用户订阅）。
		streamID := r.URL.Query().Get("stream")
		if streamID == "" {
			return errSSEUnauthorized("missing stream parameter")
		}
		if streamID != claims.Subject {
			// 源同款：记录 mismatch 并拒绝。
			log.Warn(context.Background(), "sse stream user mismatch",
				"token_subject", claims.Subject, "stream", streamID)
			return baldsse.ErrForbidden
		}
		return nil
	}
}

// errSSEUnauthorized 返回一个非 Forbidden 的 error（框架映射为 401）。
type sseUnauthorizedError struct{ msg string }

func (e sseUnauthorizedError) Error() string { return e.msg }

func errSSEUnauthorized(msg string) error { return sseUnauthorizedError{msg: msg} }

// ---- message 域 → SSE 的适配器（Wave 3.2）----

// sseMessagePublisher 把 message 域的 `Publisher` 接口适配到 `*sse.Server`。
//
// 解耦方向：message biz 只依赖窄接口（`TryPublish(streamID, eventName, payload)`），
// 不 import `bald/transport/sse`——与源 `InternalMessagePublisher` 同款。
type sseMessagePublisher struct {
	srv *baldsse.Server
}

// TryPublish 实现 msgbiz.Publisher。
//
// **用 TryPublish 而非 Publish**：非阻塞——无在线订阅者时立即返回 false，
// 不拖慢消息发送方（源 `publishNotification` 注释同此意图：
// 「TryPublish 非阻塞推送：流不存在或缓冲已满时立即跳过，避免慢客户端阻塞发送方」）。
func (p sseMessagePublisher) TryPublish(streamID, eventName string, payload any) bool {
	ok := p.srv.TryPublish(context.Background(), baldsse.StreamID(streamID),
		&baldsse.Event{
			Data:  mustJSON(payload),
			Event: []byte(eventName),
		})
	if !ok {
		// 无在线连接属正常情况（用户不在线），仅 Debug。
		log.Debug(context.Background(), "sse publish skipped (no subscriber)",
			"stream", streamID, "event", eventName)
	}
	return ok
}

// mustJSON 把 payload 序列化为 SSE 的 data 字段。
//
// 用标准库 `encoding/json` 而非 bald 的 codec：`Publish`/`TryPublish`
// 接受现成 `*Event`，不经 codec（`PublishData*` 才经 codec）。
// 此处自行序列化是为了走 TryPublish 的非阻塞语义（见上）。
func mustJSON(v any) []byte {
	b, err := stdjson.Marshal(v)
	if err != nil {
		return []byte(`{"error":"marshal failed"}`)
	}
	return b
}

// wireMessageSSE 把 SSE server 接到 message biz 上（源
// `RegisterInternalMessagePublisher` 的对应物）。
//
// sseSrv 为 nil（未装配 SSE）时返回原 biz——**降级语义**：站内信仍落库、
// 收件箱可拉取，只是没有实时推送。
func wireMessageSSE(sseSrv transport.Server, biz *msgbiz.Biz) *msgbiz.Biz {
	if sseSrv == nil || biz == nil {
		return biz
	}
	srv, ok := sseSrv.(*baldsse.Server)
	if !ok {
		log.Warn(context.Background(), "sse server type assertion failed, realtime push disabled")
		return biz
	}
	biz.SetPublisher(sseMessagePublisher{srv: srv})
	log.Info(context.Background(), "message domain wired to SSE (realtime push enabled)")
	return biz
}
