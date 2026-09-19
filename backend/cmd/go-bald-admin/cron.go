// cron.go —— Wave 2.4：cron 定时器装配。
//
// ## 装配决策（与 asynq 同款：走 WithExtraServers 逃生舱）
//
// 理由与 asynq 一致（见 asynq.go 文件头）：契约 Cron 段只有 **1 个字段**
// （`seconds`，`server.proto:224-227`），而 `bald/transport/cron` 有 4 个 Option
// ——契约驱动装配不可能，走逃生舱。
//
// ## 实测发现的两个关键约束（探针确认，记录为 D11）
//
// 1. **`NewTimerJob` 接受 6 字段 cron（含秒），与 asynq 的 5 字段相反**：
//    `bald/transport/cron/server.go:66-68` 的 parser **硬编码**包含 `cron.Second`
//    （`cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow`）。
//    因此 `"*/10 * * * * *"`（6 字段）合法，而 `"*/5 * * * *"`（5 字段）会解析失败。
//    **同一个框架内两个调度组件用不同的 cron 字段数**——极易混淆。
//
// 2. **契约的 `server.cron.seconds` 字段实际无效**：
//    `bald/transport/cron/options.go:33-38` 的 `WithSeconds` 是**空实现**
//    （函数体只有注释「默认已启用秒级，此选项保留用于未来扩展」）。
//    故契约里配 `seconds: false` 不会有任何效果——秒级始终启用。
package main

import (
	"context"
	"fmt"

	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/transport"
	"github.com/kalandramo/bald/transport/cron"
)

// buildCronServer 构造 cron 定时器服务器（Wave 2.4）。
//
// cron 无需外部依赖（进程内调度器），故**总是启用**——与 asynq 不同
// （asynq 需 Redis，无 Redis 时不启用）。
func buildCronServer(ctx context.Context) (transport.Server, error) {
	srv := cron.NewServer()
	if err := registerCronJobs(ctx, srv); err != nil {
		return nil, err
	}
	return srv, nil
}

// registerCronJobs 注册周期任务（Wave 2.4 的验收：NewTimerJob 压真实周期任务）。
//
// 目前注册一个**可观测的探针任务**（每 30 秒），用于验证装配闭环；
// 真实业务周期任务（如审计归档、租户到期扫描）在后续波次补。
func registerCronJobs(ctx context.Context, srv *cron.Server) error {
	// 注意：spec 是 **6 字段**（含秒），与 asynq 的 5 字段不同（见文件头）。
	// "*/30 * * * * *" = 每 30 秒。
	id, err := srv.NewTimerJob("*/30 * * * * *", func() {
		log.Info(ctx, "cron probe job fired")
	})
	if err != nil {
		return fmt.Errorf("cron: register probe job: %w", err)
	}
	log.Info(ctx, "cron probe job registered", "entry_id", id)
	return nil
}
