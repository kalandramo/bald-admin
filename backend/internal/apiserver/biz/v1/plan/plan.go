// Package plan 实现套餐三件套域（Wave 4.2，源 14 rpc）。
//
// 源：
//   - `i_plan.proto`（5 rpc：List/Get/Create/Update/Delete）
//   - `i_plan_module.proto`（5 rpc：List/Get/Create/Update/Delete）
//   - `i_plan_quota.proto`（4 rpc：List/Create/Update/Delete，**无 Get**）
//
// ## 级联语义（本域核心）
//
// 关系：`Plan` 1:N `PlanModule` / `PlanQuota`。
// 源用 ent Edge + `OnDelete: entsql.Cascade`（`plan.go:93,100`）——
// **删套餐时 DB 层自动级联删子表**（源 repo 的 `Delete` 只调
// `DeleteOneID`，级联由 DB 完成，见 `plan_repo.go`）。
//
// 本项目无 ent，故在 biz 层**手工级联**：删 Plan 时先删其 modules/quotas
// 再删自身（顺序重要：先子后父）——与 message 域 `DeleteMessage` 同款。
//
// **注意与 org/permgroup 的差异**：那两个域「有子节点时拒绝删除」，
// 本域**级联删除**——因为源对 Plan 配的是 Cascade 而非 Restrict。
// 语义不同是**源的差异**，不是实现随意。
package plan

import (
	"context"
	"errors"
	"fmt"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("plan: validation failed")

// ErrNotFound 资源不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 资源已存在。
var ErrConflict = store.ErrConflict

// Biz 是套餐三件套业务。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// Plan 是对外套餐。
type Plan struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Version           string `json:"version,omitempty"`
	ExpiryPolicy      string `json:"expiry_policy,omitempty"`
	DataRetentionDays int32  `json:"data_retention_days,omitempty"`
	Status            string `json:"status"`
	Remark            string `json:"remark,omitempty"`
}

// Module 是对外套餐模块。
type Module struct {
	ID     string `json:"id"`
	PlanID string `json:"plan_id"`
	Module string `json:"module"`
}

// Quota 是对外套餐配额。
type Quota struct {
	ID         string `json:"id"`
	PlanID     string `json:"plan_id"`
	QuotaType  string `json:"quota_type"`
	QuotaValue uint64 `json:"quota_value"`
}

func planID(tenantID, code string) string { return tenantID + ":plan:" + code }

// ---- Plan ----

// CreatePlan 创建套餐（源 Create）。
func (b *Biz) CreatePlan(ctx context.Context, tenantID, code string, p Plan) (*Plan, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("%w: name required", ErrValidation)
	}
	if code == "" {
		return nil, fmt.Errorf("%w: code required", ErrValidation)
	}
	status := p.Status
	if status == "" {
		status = "ON"
	}
	m := &authmodel.Plan{
		ID: planID(tenantID, code), TenantID: tenantID,
		Name: p.Name, Version: p.Version, ExpiryPolicy: p.ExpiryPolicy,
		DataRetentionDays: p.DataRetentionDays, Status: status, Remark: p.Remark,
	}
	if err := bootstrappkg.PlanStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: plan already exists: %s", ErrConflict, code)
		}
		return nil, fmt.Errorf("plan: create: %w", err)
	}
	return toPlan(m), nil
}

// GetPlan 按 id 查询（源 Get）。
func (b *Biz) GetPlan(ctx context.Context, id string) (*Plan, error) {
	m, err := bootstrappkg.PlanStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toPlan(m), nil
}

// ListPlans 列出（源 List）。
func (b *Biz) ListPlans(ctx context.Context, tenantID string) ([]Plan, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.PlanStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("plan: list: %w", err)
	}
	out := make([]Plan, 0, len(items))
	for _, m := range items {
		out = append(out, *toPlan(m))
	}
	return out, nil
}

// UpdatePlan 更新（源 Update）。
func (b *Biz) UpdatePlan(ctx context.Context, id string, p Plan) error {
	m, err := bootstrappkg.PlanStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	if p.Name != "" {
		m.Name = p.Name
	}
	if p.Version != "" {
		m.Version = p.Version
	}
	if p.ExpiryPolicy != "" {
		m.ExpiryPolicy = p.ExpiryPolicy
	}
	if p.DataRetentionDays != 0 {
		m.DataRetentionDays = p.DataRetentionDays
	}
	if p.Status != "" {
		m.Status = p.Status
	}
	if p.Remark != "" {
		m.Remark = p.Remark
	}
	if err := bootstrappkg.PlanStore.Update(ctx, m); err != nil {
		return fmt.Errorf("plan: update: %w", err)
	}
	return nil
}

// DeletePlan 删除套餐（源 Delete）——**级联删子表**。
//
// 源语义：ent Edge 配 `OnDelete: entsql.Cascade`（`plan.go:93,100`），
// 删 Plan 时 DB 自动删其 modules/quotas。
// 本项目手工实现：**先删子（modules/quotas）、再删父**——顺序重要，
// 否则父删成功而子删除失败会留下悬挂引用。
func (b *Biz) DeletePlan(ctx context.Context, id string) error {
	// 1) 级联删 modules。
	if err := bootstrappkg.PlanModuleStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("plan_id", id)},
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("plan: cascade delete modules: %w", err)
	}
	// 2) 级联删 quotas。
	if err := bootstrappkg.PlanQuotaStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("plan_id", id)},
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("plan: cascade delete quotas: %w", err)
	}
	// 3) 删父。
	if err := bootstrappkg.PlanStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("plan: delete: %w", err)
	}
	return nil
}

func toPlan(m *authmodel.Plan) *Plan {
	return &Plan{
		ID: m.ID, Name: m.Name, Version: m.Version,
		ExpiryPolicy: m.ExpiryPolicy, DataRetentionDays: m.DataRetentionDays,
		Status: m.Status, Remark: m.Remark,
	}
}

// ---- PlanModule ----

// CreateModule 给套餐加模块（源 Create）。
//
// **父套餐必须存在**（避免悬挂引用）。
func (b *Biz) CreateModule(ctx context.Context, tenantID, planID, module string) (*Module, error) {
	if planID == "" || module == "" {
		return nil, fmt.Errorf("%w: plan_id and module required", ErrValidation)
	}
	if _, err := b.GetPlan(ctx, planID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: plan not found: %s", ErrValidation, planID)
		}
		return nil, err
	}
	// 主键含 plan+module → 同一套餐同一模块重复添加自然冲突（幂等语义）。
	m := &authmodel.PlanModule{
		ID: tenantID + ":pm:" + planID + ":" + module,
		TenantID: tenantID, PlanID: planID, Module: module,
	}
	if err := bootstrappkg.PlanModuleStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: module already in plan: %s", ErrConflict, module)
		}
		return nil, fmt.Errorf("plan: create module: %w", err)
	}
	return &Module{ID: m.ID, PlanID: m.PlanID, Module: m.Module}, nil
}

// ListModules 列出某套餐的模块（源 List）。
func (b *Biz) ListModules(ctx context.Context, planID string) ([]Module, error) {
	items, _, err := bootstrappkg.PlanModuleStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("plan_id", planID)},
	})
	if err != nil {
		return nil, fmt.Errorf("plan: list modules: %w", err)
	}
	out := make([]Module, 0, len(items))
	for _, m := range items {
		out = append(out, Module{ID: m.ID, PlanID: m.PlanID, Module: m.Module})
	}
	return out, nil
}

// GetModule 按 id 查询（源 Get）。
func (b *Biz) GetModule(ctx context.Context, id string) (*Module, error) {
	m, err := bootstrappkg.PlanModuleStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return &Module{ID: m.ID, PlanID: m.PlanID, Module: m.Module}, nil
}

// UpdateModule 更新（源 Update）——模块标识可改。
func (b *Biz) UpdateModule(ctx context.Context, id, module string) error {
	if module == "" {
		return fmt.Errorf("%w: module required", ErrValidation)
	}
	m, err := bootstrappkg.PlanModuleStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	m.Module = module
	if err := bootstrappkg.PlanModuleStore.Update(ctx, m); err != nil {
		return fmt.Errorf("plan: update module: %w", err)
	}
	return nil
}

// DeleteModule 删除模块（源 Delete）。
func (b *Biz) DeleteModule(ctx context.Context, id string) error {
	if err := bootstrappkg.PlanModuleStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("plan: delete module: %w", err)
	}
	return nil
}

// ---- PlanQuota ----

// CreateQuota 给套餐加配额（源 Create）。
func (b *Biz) CreateQuota(ctx context.Context, tenantID, planID, quotaType string, value uint64) (*Quota, error) {
	if planID == "" || quotaType == "" {
		return nil, fmt.Errorf("%w: plan_id and quota_type required", ErrValidation)
	}
	if _, err := b.GetPlan(ctx, planID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: plan not found: %s", ErrValidation, planID)
		}
		return nil, err
	}
	m := &authmodel.PlanQuota{
		ID: tenantID + ":pq:" + planID + ":" + quotaType,
		TenantID: tenantID, PlanID: planID,
		QuotaType: quotaType, QuotaValue: value,
	}
	if err := bootstrappkg.PlanQuotaStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: quota already exists: %s", ErrConflict, quotaType)
		}
		return nil, fmt.Errorf("plan: create quota: %w", err)
	}
	return &Quota{ID: m.ID, PlanID: m.PlanID, QuotaType: m.QuotaType, QuotaValue: m.QuotaValue}, nil
}

// ListQuotas 列出某套餐的配额（源 List）。
func (b *Biz) ListQuotas(ctx context.Context, planID string) ([]Quota, error) {
	items, _, err := bootstrappkg.PlanQuotaStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("plan_id", planID)},
	})
	if err != nil {
		return nil, fmt.Errorf("plan: list quotas: %w", err)
	}
	out := make([]Quota, 0, len(items))
	for _, m := range items {
		out = append(out, Quota{ID: m.ID, PlanID: m.PlanID, QuotaType: m.QuotaType, QuotaValue: m.QuotaValue})
	}
	return out, nil
}

// UpdateQuota 更新配额值（源 Update）。
func (b *Biz) UpdateQuota(ctx context.Context, id string, value uint64) error {
	m, err := bootstrappkg.PlanQuotaStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	m.QuotaValue = value
	if err := bootstrappkg.PlanQuotaStore.Update(ctx, m); err != nil {
		return fmt.Errorf("plan: update quota: %w", err)
	}
	return nil
}

// DeleteQuota 删除配额（源 Delete）。
func (b *Biz) DeleteQuota(ctx context.Context, id string) error {
	if err := bootstrappkg.PlanQuotaStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("plan: delete quota: %w", err)
	}
	return nil
}
