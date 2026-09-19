// sse.go —— Wave 3.1：SSE 传输轴装配。
//
// ## 装配路径：逃生舱（同 asynq / cron）
//
// 契约 `server.sse` 段**存在**（`server.proto:151-157`：`addr`/`path`/`tls`，
// 3 字段），但 **bald 框架不装配它**——`bald/bootstrap/server.go:93` 明确标注
// `{"sse", false, ...}`，且 `bald/bootstrap/server_sections_test.go:20-40`
// 断言「配了 `server.sse` 必须**报错**，错误信息须点名该段」。
//
// 这是 **D2 族的又一实例**：契约段声明了，但实现没接（与 D5 `rate_limit`、
// D11.2 `cron.seconds` 同族）。故本装配走 `WithExtraServers` 逃生舱，
// 直接读环境变量（而非依赖框架的契约驱动装配）。
//
// ## 与源项目的对照
//
// 源用 `kratos-transport/transport/sse`，经 `sse.NewSseServer(cfg.Server.Sse, ...)`
// 装配，并以 `WithSubscriberFunction` / `WithAuthorizeFunc` 注入
// message 域的 `HandleSubscribe` / `HandleAuthorize`。
// bald 的 `transport/sse` 提供**同名同形的两个 Option**（`options.go:98-114`），
// 故形态可对齐——只是装配入口不同（源靠框架、本项目靠逃生舱）。
package main

import (
	"context"
	"net/http"
	"os"

	"github.com/kalandramo/bald/encoding"
	"github.com/kalandramo/bald/encoding/json"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/transport"
	baldsse "github.com/kalandramo/bald/transport/sse"
)

// init 注册 json codec —— SSE 的 PublishData* 依赖 encoding 全局注册表。
//
// **与 asynq 同族约束**（Wave 2.1 已踩过）：bald 的 `encoding` 是全局注册表，
// `NewServer` 内 `GetCodec("json")` 在未注册时返回 nil 且**不报错**，
// 直到 `PublishData*` 调用才返回
// `sse: codec is nil (nothing registered); register one via encoding.MustRegister`。
// 故必须在装配期注册（MustRegister 幂等，重复注册同 name 会覆盖）。
func init() {
	encoding.MustRegister(json.New())
}

// buildSSEServer 构造 SSE 服务器（Wave 3.1）。
//
// 返回 (nil, nil) 表示「未配置 SSE」——与 asynq 的「无 Redis 不启用」同款
// 可选语义（判据不同：asynq 看依赖可达性，SSE 看配置是否显式声明）。
func buildSSEServer(ctx context.Context, cfg sseConfig) (transport.Server, error) {
	if cfg.Addr == "" {
		return nil, nil // 未配置 → 不启用
	}
	if cfg.Path == "" {
		cfg.Path = "/events" // 与契约默认值一致（server.proto:154）
	}

	opts := []baldsse.Option{
		baldsse.WithPath(cfg.Path),
		// **关键约束（探针实测）**：`autoStream` 默认 false，
		// 而 `ServeHTTP` 在「流不存在 && autoStream=false」时返回
		// 500 "Stream not found!"（`http.go:79-83`）。故订阅方必须先
		// 手工 CreateStream，否则首连必 500。开启 autoStream 让首次订阅
		// 自动建流——这是可用的默认行为。
		baldsse.WithAutoStream(true),
		// 断线重连补发（SSE 协议标准语义，源同样具备）。
		baldsse.WithAutoReplay(true),
		// 订阅授权钩子。签名已核实：func(*http.Request, string) error
		// （bald/transport/sse/auth.go:19）。
		baldsse.WithAuthorizeFunc(sseAuthorize),
	}

	srv := baldsse.NewServer(cfg.Addr, opts...)
	log.Info(ctx, "sse server built", "addr", cfg.Addr, "path", cfg.Path)
	return srv, nil
}

// sseAuthorize 是 SSE 订阅授权钩子（Wave 3.1）。
//
// **当前为占位实现**：完整实现需校验 token 并把用户身份绑定到 streamID
// （源的做法是 `internalMessageService.HandleAuthorize`）。
// 本波先打通装配闭环，授权接线在 3.2 与 message 域一起做。
// **不假装已实现**——日志显式标注 placeholder。
func sseAuthorize(r *http.Request, token string) error {
	log.Debug(context.Background(), "sse authorize called (placeholder, 待 3.2 接线)",
		"path", r.URL.Path)
	return nil
}

// sseConfig 是 SSE 装配配置（逃生舱下自读环境变量）。
//
// 与项目既有约定一致：配置走环境变量注入（`BALD_ADMIN_*`），
// 避免依赖含云端凭据的 `configs/go-bald-admin.yaml`。
type sseConfig struct {
	Addr string
	Path string
}

func loadSSEConfig() sseConfig {
	return sseConfig{
		Addr: os.Getenv("BALD_ADMIN_SSE_ADDR"),
		Path: os.Getenv("BALD_ADMIN_SSE_PATH"),
	}
}
