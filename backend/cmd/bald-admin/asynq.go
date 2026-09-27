// asynq.go —— Wave 2.1：asynq 任务队列装配。
//
// ## 装配决策（计划 2.1/2.2 要求二选一，此处记录结论）
//
// **选「main 手工 NewServer + appkit.WithExtraServers」，不扩契约段。**
//
// 理由（三条，均基于实测）：
//  1. `bald/transport/asynq` **无 contract 子包**——appkit 的契约驱动装配
//     （ServerProvider 注册表）无法覆盖它，框架本身要求走逃生舱；
//  2. 契约 `server.proto` 的 Asynq 段只有 **3 个字段**
//     （redis_address / redis_password / redis_db），而实现有 **约 30 个 Option**
//     （WithConcurrency / WithQueues / WithStrictPriority / 各种超时…）——
//     3 字段不足以驱动真实装配；
//  3. 计划 §R5 已定调「**倾向先记录后扩**，避免为凑装配而设计契约」，
//     且 Wave 0.2 的教训是「契约段配了但无实现应 fail-fast」——
//     扩契约需框架发版周期，超出本轮范围。
//
// 逃生舱是框架**明确提供**的（`appkit.WithExtraServers`，
// `bald/pkg/appkit/bootstrap.go:186`），且本仓已有先例（gateway 第三服务器，
// `main.go:232/421`）——与 `pkg/appkit/bootstrap.go:392-396` 注释载明的
// 「能力声明在代码，是刻意的」一致。
//
// **已记录缺陷 D8**：契约 Asynq 段字段面 << 实现 Option 面，契约驱动装配不可能。
package main

import (
	"context"
	"fmt"
	"os"

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

// buildAsynqServer 构造 asynq 任务服务器（Wave 2.1）。
//
// 返回 nil 表示未启用（无 Redis 地址）——调用方跳过注册，任务域退化为不可用
// （与 file/redis 的同款降级语义：外部依赖缺失时该能力不可用，但**启动不阻断**）。
//
// **处理器在构造期注册**（不是 AfterStart）：asynq 的 handler 是**启动期配置**
// ——`Start` 时绑定到 mux，启动后再注册无效（任务会被判为「无处理器」）。
// 这是实测确认的时序约束（首版误放 AfterStart，任务入队后无消费者）。
//
// Wave 5.5：codec 由配置驱动（`asynq.codec`，默认 json）——压 `bald/encoding`
// 轴（msgpack/proto 等替代 json）。**注册先于 WithCodec**：asynq 的
// `WithCodec(name)` 内部 `encoding.GetCodec(name)` 对未注册名**静默设 nil**，
// 故障延迟到首次入队才以 `codec is nil` 暴露——故此处显式注册后再传名。
func buildAsynqServer(ctx context.Context, redisAddr string) (transport.Server, error) {
	if redisAddr == "" {
		return nil, nil
	}
	registerAsynqCodecs()

	codecName := asynqCodecName()
	srv := asynq.NewServer(
		asynq.WithRedisAddress(redisAddr),
		// 并发度：契约段无此字段，正是「字段面不足」的体现（见文件头决策）。
		asynq.WithConcurrency(4),
		asynq.WithCodec(codecName),
	)
	if err := registerAsynqHandlers(ctx, srv); err != nil {
		return nil, err
	}
	return srv, nil
}

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

// asynqCodecName 返回 asynq 使用的 codec 名（配置 `asynq.codec`，默认 json）。
//
// 与 asynqRedisAddr 同款时序约束：本函数在 FromBootstrap **之前**调用（构造期），
// 配置 store 尚未就绪，故读 env（`BALD_ADMIN_ASYNQ_CODEC`）作可靠路径。
// 未配置时返回 "json"——与框架默认（server.go:122）一致，行为零回归。
func asynqCodecName() string {
	if v := os.Getenv("BALD_ADMIN_ASYNQ_CODEC"); v != "" {
		return v
	}
	return "json"
}

// asynqRedisAddr 返回 asynq 用的 Redis 地址。
//
// 复用 bootstrap 的 Redis 解析结果（`bootstrappkg.RedisClient` 的 Options），
// 避免 asynq 与业务缓存连到不同实例——**同源**是这里的关键：任务队列与
// 缓存共用一个 Redis 是常见部署，但若配置漂移会导致「入队到 A、消费从 B」。
//
// 注意时序：本函数在 FromBootstrap **之前**调用（构造期），此时 RedisClient
// 可能尚未装配（BeforeStart 才赋值）。故优先读 env，回退到空（不启用）。
// 这是「构造期配置未就绪」的既有约束（与 file.SetStorage 同款时序问题）——
// 用 env 是可靠路径，契约段读取需等 BeforeStart。
func asynqRedisAddr() string {
	// 与 bootstrap.resolveRedis 同源：env 优先。
	if a := os.Getenv("BALD_ADMIN_REDIS_ADDR"); a != "" {
		return a
	}
	return ""
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
