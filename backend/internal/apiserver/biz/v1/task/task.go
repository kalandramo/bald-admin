// Package task 实现任务调度域（Wave 2.3，源 655 行，Wave 2 最大业务域）。
//
// 复刻范围（源 11 rpc）：List/Count/Get/Create/Update/Delete/ListTaskTypeName/
// RestartAllTask/StartAllTask/StopAllTask/ControlTask。
//
// ## 核心设计（对齐源）
//
// 源用 **TaskScheduler 接口 + hasScheduler() nil 检查**：asynq 未配置时
// taskScheduler 为 nil，**每个调用点前置检查并返回明确错误**，而非 nil 解引用
// panic（源 `task_service.go:100-104` 注释原话「避免 nil 解引用 panic」）。
// 本实现逐条对齐——这是「外部依赖可选」的正确姿势。
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("task: validation failed")

// ErrNotFound 任务不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 任务已存在。
var ErrConflict = store.ErrConflict

// ErrNoScheduler 调度器未配置（asynq 不可用）。
// **这是显式错误而非静默降级**——调用方必须知道任务不会被调度。
var ErrNoScheduler = errors.New("task: scheduler is not configured")

// 任务类型常量（对齐源 task.proto 的 Task.Type）。
const (
	TypePeriodic   = "PERIODIC"
	TypeDelay      = "DELAY"
	TypeWaitResult = "WAIT_RESULT"
)

// Scheduler 是任务调度抽象（对齐源的 TaskScheduler 接口）。
//
// 由 main 装配时注入（asynq server 适配）；未注入时 nil——
// 所有调用点经 hasScheduler() 前置检查。
type Scheduler interface {
	// NewTask 投递一次性任务。
	NewTask(typeName string, payload any, opts ...any) error
	// NewPeriodicTask 注册周期任务，返回 entryID（用于后续停止）。
	NewPeriodicTask(cronSpec, typeName string, payload any, opts ...any) (string, error)
	// RemovePeriodicTask 移除周期任务。
	//
	// **参数是 taskId（任务类型名），不是 entryID**——实测发现：
	// bald 的 asynq.Server.RemovePeriodicTask 期望 taskId，内部自行反查 entryID
	// （`server.go:671-680`）。首版传 entryID 导致「periodic task not found」。
	RemovePeriodicTask(taskID string) error
	// RegisteredTypes 返回已注册的任务处理器类型（供 ListTaskTypeName）。
	RegisteredTypes() []string
}

// Biz 是任务调度业务。
type Biz struct {
	scheduler Scheduler
	// running 记录运行中的任务（typeName → entryID）。
	running map[string]string
}

// New 构造 Biz。
func New() *Biz { return &Biz{running: map[string]string{}} }

// SetScheduler 运行期注入调度器（nil 不覆盖）。
func (b *Biz) SetScheduler(s Scheduler) {
	if s != nil {
		b.scheduler = s
	}
}

// hasScheduler 检查调度器是否可用（对齐源的 nil 防护）。
func (b *Biz) hasScheduler() bool { return b.scheduler != nil }

// Task 是对外的任务结构。
type Task struct {
	ID          string `json:"id"`
	TypeName    string `json:"type_name"`
	Type        string `json:"type"`
	CronSpec    string `json:"cron_spec,omitempty"`
	TaskPayload string `json:"task_payload,omitempty"`
	Enable      bool   `json:"enable"`
	TaskOptions string `json:"task_options,omitempty"`
	Remark      string `json:"remark,omitempty"`
	Running     bool   `json:"running"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

// validateCronSpec 校验 cron 表达式（Wave 2.3）。
//
// **实测发现的约束**：asynq 的 `NewPeriodicTask` 用**标准 5 字段** cron
// （`分 时 日 月 周`），传 6 字段（含秒，如 `*/2 * * * * *`）会在注册时失败
// （asynq 内部报 `expected exactly 5 fields, found 6`）。
//
// 端到端验证时这个错误以 **500** 返回（被当作服务端故障）——但它是**用户输入
// 错误**，应在入口处校验并归 400。这是「校验前移」的价值：让错误在正确的层
// 以正确的状态码暴露。
func validateCronSpec(spec string) error {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return fmt.Errorf("%w: cron spec must have exactly 5 fields (min hour day month weekday), got %d: %q",
			ErrValidation, len(fields), spec)
	}
	return nil
}

// CreateTask 创建任务定义（源 Create）。校验类型与周期表达式。
func (b *Biz) CreateTask(ctx context.Context, tenantID string, t Task) (*Task, error) {
	if t.TypeName == "" {
		return nil, fmt.Errorf("%w: type_name required", ErrValidation)
	}
	switch t.Type {
	case TypePeriodic:
		if t.CronSpec == "" {
			return nil, fmt.Errorf("%w: cron_spec required for PERIODIC task", ErrValidation)
		}
		if err := validateCronSpec(t.CronSpec); err != nil {
			return nil, err
		}
	case TypeDelay, TypeWaitResult:
		// 无需 cronSpec
	default:
		return nil, fmt.Errorf("%w: unknown task type %q", ErrValidation, t.Type)
	}
	m := &authmodel.Task{
		ID: t.TypeName, TenantID: tenantID, TypeName: t.TypeName, Type: t.Type,
		CronSpec: t.CronSpec, TaskPayload: t.TaskPayload, Enable: t.Enable,
		TaskOptions: t.TaskOptions, Remark: t.Remark,
	}
	if err := bootstrappkg.TaskStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: task already exists", ErrConflict)
		}
		return nil, fmt.Errorf("task: create: %w", err)
	}
	return toTask(m), nil
}

// GetTask 查询（源 Get）。
func (b *Biz) GetTask(ctx context.Context, typeName string) (*Task, error) {
	m, err := bootstrappkg.TaskStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", typeName)},
	})
	if err != nil {
		return nil, err
	}
	t := toTask(m)
	t.Running = b.isRunning(typeName)
	return t, nil
}

// ListTasks 列出（源 List）。
func (b *Biz) ListTasks(ctx context.Context, tenantID string) ([]Task, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.TaskStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("task: list: %w", err)
	}
	out := make([]Task, 0, len(items))
	for _, m := range items {
		t := toTask(m)
		t.Running = b.isRunning(m.TypeName)
		out = append(out, *t)
	}
	return out, nil
}

// CountTasks 统计（源 Count）。
func (b *Biz) CountTasks(ctx context.Context, tenantID string) (int64, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	n, err := bootstrappkg.TaskStore.Count(ctx, w)
	if err != nil {
		return 0, fmt.Errorf("task: count: %w", err)
	}
	return n, nil
}

// UpdateTask 更新（源 Update）。
func (b *Biz) UpdateTask(ctx context.Context, typeName string, t Task) error {
	m, err := bootstrappkg.TaskStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", typeName)},
	})
	if err != nil {
		return err
	}
	if t.CronSpec != "" {
		m.CronSpec = t.CronSpec
	}
	if t.TaskPayload != "" {
		m.TaskPayload = t.TaskPayload
	}
	if t.Remark != "" {
		m.Remark = t.Remark
	}
	m.Enable = t.Enable
	if err := bootstrappkg.TaskStore.Update(ctx, m); err != nil {
		return fmt.Errorf("task: update: %w", err)
	}
	return nil
}

// DeleteTask 删除（源 Delete）。运行中先停止。
func (b *Biz) DeleteTask(ctx context.Context, typeName string) error {
	if b.isRunning(typeName) {
		_ = b.StopTask(ctx, typeName)
	}
	if err := bootstrappkg.TaskStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", typeName)},
	}); err != nil {
		return fmt.Errorf("task: delete: %w", err)
	}
	return nil
}

// ListTaskTypeNames 列出**已注册的处理器类型**（源 ListTaskTypeName）。
//
// 语义：返回 asynq 已注册 handler 的 taskType 列表——调用方据此知道
// 「哪些任务类型真的有处理器」。无调度器时返回空（而非报错）——
// 这是「查询能力」而非「执行操作」，空列表是诚实的答案。
func (b *Biz) ListTaskTypeNames(ctx context.Context) []string {
	if !b.hasScheduler() {
		return []string{}
	}
	return b.scheduler.RegisteredTypes()
}

// StartTask 启动单个任务（源 startTask 的对外入口，经 ControlTask 调用）。
func (b *Biz) StartTask(ctx context.Context, typeName string) error {
	if !b.hasScheduler() {
		return ErrNoScheduler
	}
	m, err := bootstrappkg.TaskStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", typeName)},
	})
	if err != nil {
		return err
	}
	if !m.Enable {
		return fmt.Errorf("%w: task is not enabled", ErrValidation)
	}
	var payload any
	if m.TaskPayload != "" {
		_ = json.Unmarshal([]byte(m.TaskPayload), &payload)
	}
	switch m.Type {
	case TypePeriodic:
		// 启动时再校验一次：任务可能是在校验逻辑加入前创建的（存量数据），
		// 或经 Update 改了 cronSpec——注册失败归 ErrValidation（用户输入问题）
		// 而非 500（服务端故障）。
		if err := validateCronSpec(m.CronSpec); err != nil {
			return err
		}
		entryID, err := b.scheduler.NewPeriodicTask(m.CronSpec, m.TypeName, payload)
		if err != nil {
			return fmt.Errorf("%w: start periodic: %s", ErrValidation, err)
		}
		b.running[m.TypeName] = entryID
	case TypeDelay, TypeWaitResult:
		if err := b.scheduler.NewTask(m.TypeName, payload); err != nil {
			return fmt.Errorf("task: start: %w", err)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrValidation, m.Type)
	}
	return nil
}

// StopTask 停止单个任务。
func (b *Biz) StopTask(ctx context.Context, typeName string) error {
	if !b.hasScheduler() {
		return ErrNoScheduler
	}
	if _, ok := b.running[typeName]; !ok {
		return nil // 未运行：幂等成功（源同此语义）
	}
	// 传 typeName（taskId）而非 entryID——见接口注释。
	if err := b.scheduler.RemovePeriodicTask(typeName); err != nil {
		return fmt.Errorf("task: stop: %w", err)
	}
	delete(b.running, typeName)
	return nil
}

// StartAllTasks 启动全部启用任务（源 StartAllTask）。
// 返回成功启动的数量。
func (b *Biz) StartAllTasks(ctx context.Context) (int, error) {
	if !b.hasScheduler() {
		return 0, ErrNoScheduler
	}
	items, _, err := bootstrappkg.TaskStore.List(ctx, &store.Where{})
	if err != nil {
		return 0, fmt.Errorf("task: list for start: %w", err)
	}
	n := 0
	for _, m := range items {
		if !m.Enable {
			continue
		}
		if err := b.StartTask(ctx, m.TypeName); err != nil {
			// 单个失败不阻断其余（源同此：记录并继续）。
			continue
		}
		n++
	}
	return n, nil
}

// StopAllTasks 停止全部运行中任务（源 StopAllTask）。
func (b *Biz) StopAllTasks(ctx context.Context) {
	for typeName := range b.running {
		_ = b.StopTask(ctx, typeName)
	}
}

// RestartAllTasks 重启全部（源 RestartAllTask）：先停后启。
func (b *Biz) RestartAllTasks(ctx context.Context) (int, error) {
	b.StopAllTasks(ctx)
	return b.StartAllTasks(ctx)
}

// ControlTask 控制单个任务启停（源 ControlTask）。
func (b *Biz) ControlTask(ctx context.Context, typeName string, start bool) error {
	if start {
		return b.StartTask(ctx, typeName)
	}
	return b.StopTask(ctx, typeName)
}

func (b *Biz) isRunning(typeName string) bool {
	_, ok := b.running[typeName]
	return ok
}

func toTask(m *authmodel.Task) *Task {
	t := &Task{
		ID: m.ID, TypeName: m.TypeName, Type: m.Type, CronSpec: m.CronSpec,
		TaskPayload: m.TaskPayload, Enable: m.Enable, TaskOptions: m.TaskOptions,
		Remark: m.Remark,
	}
	if !m.CreatedAt.IsZero() {
		t.CreatedAt = m.CreatedAt.Unix()
	}
	return t
}

// 保留 time 引用。
var _ = time.Now
