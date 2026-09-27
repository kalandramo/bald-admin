package main

import (
	"fmt"

	hibiken "github.com/hibiken/asynq"

	"github.com/kalandramo/bald/transport/asynq"

	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
)

// asynqScheduler 把 bald 的 asynq.Server 适配为 task.Scheduler 接口（Wave 2.3）。
//
// ## 一个必须说清的命名陷阱（实测发现，天权称量后记录）
//
// **存在两个同名但语义完全不同的 `asynq.Option`**：
//   - `github.com/kalandramo/bald/transport/asynq.Option` = `func(*Server)`
//     —— **服务器构造选项**（WithConcurrency/WithQueues/WithRedisAddress…）；
//   - `github.com/hibiken/asynq.Option` —— **任务投递选项**
//     （MaxRetry/Timeout/ProcessIn/Retention…）。
//
// 而 `bald/transport/asynq.Server.NewTask` 的签名收的是**后者**：
// `NewTask(typeName string, msg any, opts ...hibiken.Option)`。
// 首版误以为是前者，编译期即被拦下（类型不匹配）——这是「同名类型跨包」
// 的典型陷阱，记于此供后来者避坑。
//
// 因此本适配器的 `opts ...any` 期望的是 `hibiken/asynq.Option`。
type asynqScheduler struct {
	srv *asynq.Server
}

// newAsynqScheduler 构造适配器。
func newAsynqScheduler(srv *asynq.Server) taskbiz.Scheduler {
	return &asynqScheduler{srv: srv}
}

// NewTask 实现 task.Scheduler。
func (a *asynqScheduler) NewTask(typeName string, payload any, opts ...any) error {
	return a.srv.NewTask(typeName, payload, toTaskOpts(opts)...)
}

// NewPeriodicTask 实现 task.Scheduler。
func (a *asynqScheduler) NewPeriodicTask(cronSpec, typeName string, payload any, opts ...any) (string, error) {
	return a.srv.NewPeriodicTask(cronSpec, typeName, payload, toTaskOpts(opts)...)
}

// RemovePeriodicTask 实现 task.Scheduler。
func (a *asynqScheduler) RemovePeriodicTask(taskID string) error {
	// 注意：bald 的 RemovePeriodicTask 期望 **taskId**（任务类型名），
	// 内部自行反查 entryID——不是 entryID（实测踩坑，见接口注释）。
	return a.srv.RemovePeriodicTask(taskID)
}

// RegisteredTypes 实现 task.Scheduler：返回已注册 handler 的 taskType 列表。
func (a *asynqScheduler) RegisteredTypes() []string {
	return a.srv.GetRegisteredTaskTypes()
}

// toTaskOpts 把 ...any 转为 ...hibiken.Option（逐项断言）。
//
// 非 hibiken.Option 的项被**跳过并记日志**——不静默吞（否则调用方误以为选项生效）。
func toTaskOpts(opts []any) []hibiken.Option {
	out := make([]hibiken.Option, 0, len(opts))
	for _, o := range opts {
		if ao, ok := o.(hibiken.Option); ok {
			out = append(out, ao)
		} else {
			fmt.Printf("asynq scheduler: ignoring non-task Option arg %T\n", o)
		}
	}
	return out
}
