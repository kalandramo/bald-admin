package model

import "time"

// Task 任务定义（Wave 2.3，自源 task.proto 精简移植）。
//
// 语义：一条 Task 是**任务的定义**（类型/调度表达式/参数），不是某次执行。
// 按 Type 分派到 asynq：
//   - PERIODIC：周期任务（用 cronSpec，委托 NewPeriodicTask）
//   - DELAY：延迟一次性任务（委托 NewTask）
//   - WAIT_RESULT：等待结果的任务（委托 NewWaitResultTask）
//
// 主键用业务键 `"<type_name>"`：源的 type_name 是任务处理器的唯一标识
// （如 "audit:archive"），与 asynq 的 taskType 一一对应——同一 type 只应有一个
// 任务定义（否则重复调度）。
type Task struct {
	ID       string `gorm:"primaryKey"` // = TypeName
	TenantID string `gorm:"index"`
	// TypeName 任务类型名（asynq 的 taskType，如 "audit:archive"）。
	TypeName string `gorm:"index"`
	// Type 任务类型：PERIODIC / DELAY / WAIT_RESULT。
	Type string
	// CronSpec 周期表达式（仅 PERIODIC 用，如 "*/5 * * * *"）。
	CronSpec string
	// TaskPayload 任务参数（JSON 文本）。
	TaskPayload string
	// Enable 是否启用（源语义：未启用不参与 startAll）。
	Enable bool
	// TaskOptions 执行选项（JSON 文本：max_retry/timeout/process_in/…）。
	TaskOptions string
	// Remark 备注。
	Remark string
	// 运行态（由 scheduler 回填，非持久化）。
	Running   bool `gorm:"-"`
	EntryID   string `gorm:"-"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
