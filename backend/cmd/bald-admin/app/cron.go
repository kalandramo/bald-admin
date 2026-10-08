// cron.go —— cron 定时器装配（业务侧：周期任务注册）。
//
// ⚠️ **本节原记的装配决策已被取代**（保留作历史记录）：
// 原决策＝「与 asynq 同款，走 WithExtraServers 逃生舱」。
// **当前装配路径已是契约驱动**：
//
//	registries.go → sr.MustRegister(croncontract.Type, croncontract.Provider(
//	                    croncontract.WithJobs(registerCronJobs)))
//	assembly.go   → appkit.WithServerRegistry(serverRegistry(...))
//
// 取代原因：契约 Cron 段字段已由 1 个扩到 **3 个**
// （seconds、gracefully_shutdown、location），且
// `bald/transport/cron/contract` 子包**已建立**（含 `WithJobs` 回调挂载周期任务）
// ——契约驱动装配可行。
//
// ## 原记的两个关键约束（其一已由本轮修复闭合）
//
//  1. **6 字段 cron 表达式**（含秒）——设计如此，非缺陷：`cron.NewServer` 的
//     parser 默认含 `cron.Second`；`WithSeconds(false)` 可关闭（只收 5 字段）。
//     与 asynq 默认 5 字段的差异是**刻意的**（asynq 走 robfig 标准语法）。
//     ⇒ 本文件下方注册任务用 6 字段表达式是正确的。
//
//  2. ~~契约 `server.cron.seconds` 字段无效~~ —— **已修**：
//     `WithSeconds` 曾是空实现（函数体只有注释），现已落地为
//     `func(o *options) { o.seconds = enable }`，契约配 `seconds: false` 真正生效。
//     （原记缺陷 D11.2 已闭合。）
package app

import (
	"context"
	"fmt"

	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/transport/cron"
)

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
