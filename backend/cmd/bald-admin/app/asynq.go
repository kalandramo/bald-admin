// asynq.go —— asynq 任务队列装配（业务侧：codec 注册 + handler 注册）。
//
// ⚠️ **本节原记的装配决策已被取代**（保留作历史记录）：
// 原决策＝「main 手工 NewServer + appkit.WithExtraServers，不扩契约段」。
// **当前装配路径已是契约驱动**：
//
//	registries.go → sr.MustRegister(asynqcontract.Type, asynqcontract.Provider(...))
//	assembly.go   → appkit.WithServerRegistry(serverRegistry(...))
//
// 取代原因（框架侧两轮变更）：
//  1. `bald/transport/asynq/contract` 子包**已建立**（契约→Option 映射 + Provider）；
//  2. 契约 Asynq 段字段由 3 个扩到 **10 个**（redis_address/password/db、concurrency、
//     queues、codec、strict_priority、shutdown_timeout_ms、gracefully_shutdown、
//     scheduler_enabled）——足以驱动常用装配；其余 Option 经
//     `asynqcontract.WithServerOptions` 补齐；
//  3. `bootstrap.validateServerSections` 已支持「段已注册 provider 即放行」
//     （签名含 registered map）——使 implemented=false 的段也能走契约装配。
//
// 历史决策原文（保留）：曾选「不扩契约段、走逃生舱」，理由为
//
//	①`bald/transport/asynq` 无 contract 子包；②契约段仅 3 字段 << 实现约 30 个
//	Option；③计划 §R5 定调「倾向先记录后扩，避免为凑装配而设计契约」。
//
// 所记缺陷 **D8**（契约 Asynq 段字段面 << 实现 Option 面）已随字段扩展闭合。
//
// 逃生舱本身仍是框架**明确提供**的合法扩展点
// （`appkit.WithExtraServers` / `WithExtraServerFunc`，见 pkg/appkit/bootstrap.go），
// 只是 asynq 不再需要它。
//
// 本文件保留的实装：codec 注册（`registerAsynqCodecs`）与 handler 注册
// （`registerAsynqHandlers`）——两者的**调用点**已移入 registries.go 的 provider 回调。
package app

import (
	"context"
	"fmt"

	"github.com/kalandramo/bald/encoding"
	"github.com/kalandramo/bald/encoding/json"
	"github.com/kalandramo/bald/encoding/msgpack"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/transport"
	"github.com/kalandramo/bald/transport/asynq"
)

// asynqTaskCodecOnce 保证 codec 只注册一次。
//
// **实测发现的硬约束**（探针验证）：asynq 的 `NewTask` 依赖全局 codec
// （`encoding.MustRegister`），未注册时入队直接报
// `codec is nil (nothing registered)`。这是集成前置条件，漏了会在运行期
// 才暴露（且是「入队失败」而非启动失败）——故在装配期显式注册。
var asynqCodecRegistered bool

// registerAsynqCodecs 注册 asynq 可用的全部 codec（幂等）。
//
// 注册 json 与 msgpack 两个：json 是缺省（`server.go:122` 默认 GetCodec("json")），
// msgpack 供 `asynq.codec: msgpack` 配置切换。两者都注册使配置切换无需额外装配。
func registerAsynqCodecs() {
	if asynqCodecRegistered {
		return
	}
	encoding.MustRegister(json.New())
	encoding.MustRegister(msgpack.New())
	asynqCodecRegistered = true
}

// registerAsynqHandlers 注册任务处理器（Wave 2.3 task 域消费）。
//
// 目前注册一个**可观测的探针任务**（`bald:probe`），用于验证装配闭环；
// 真实业务任务（task 域的调度执行）在 Wave 2.3 补。
func registerAsynqHandlers(ctx context.Context, srv transport.Server) error {
	as, ok := srv.(*asynq.Server)
	if !ok {
		return fmt.Errorf("asynq: unexpected server type %T", srv)
	}
	// 探针任务：仅记日志，证明「入队→消费→完成」闭环。
	type probePayload struct {
		Msg string `json:"msg"`
	}
	// 注意签名：RegisterSubscriberWithCtx 的 handler 是 (ctx, taskType, *T)。
	if err := asynq.RegisterSubscriberWithCtx(as, "bald:probe",
		func(c context.Context, taskType string, p *probePayload) error {
			log.Info(c, "asynq probe task consumed", "type", taskType, "msg", p.Msg)
			return nil
		}); err != nil {
		return fmt.Errorf("asynq: register probe handler: %w", err)
	}
	return nil
}
