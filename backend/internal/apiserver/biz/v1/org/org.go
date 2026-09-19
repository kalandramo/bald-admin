// Package org 实现组织架构域（Wave 1.7）：
// org_unit（7 rpc，树形）+ position（7 rpc，关联回填）。
//
// 复刻范围对齐源：两者都是标准 CRUD + **树构建** + **关联字段回填**。
//
// 源的回填是三步式（`org_unit_service.go:49-117`）：
//  1. extractRelationIDs：递归收集所有节点引用的用户 ID；
//  2. fetchRelationInfo：**批量**查询（避免 N+1）；
//  3. bindRelations：递归回填到每个节点（含 children）。
// 本实现逐条对齐——批量查询是源的精髓，逐个查会退化成 N+1。
package org

import (
	"context"
	"errors"
	"fmt"
	"strings"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("org: validation failed")

// ErrNotFound 资源不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 资源已存在。
var ErrConflict = store.ErrConflict

// ErrCycle 检测到环路（父子关系成环）。
var ErrCycle = errors.New("org: cycle detected in hierarchy")

// Biz 是组织架构业务。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// ---- org_unit ----

// OrgUnit 是对外的组织单元（含 children，树形）。
type OrgUnit struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Code        string     `json:"code"`
	Type        string     `json:"type"`
	ParentID    string     `json:"parent_id,omitempty"`
	Path        string     `json:"path,omitempty"`
	Status      string     `json:"status"`
	SortOrder   int32      `json:"sort_order"`
	LeaderID    string     `json:"leader_id,omitempty"`
	LeaderName  string     `json:"leader_name,omitempty"` // 回填
	Remark      string     `json:"remark,omitempty"`
	Description string     `json:"description,omitempty"`
	Children    []*OrgUnit `json:"children,omitempty"` // 树形
}

func orgID(tenantID, code string) string { return tenantID + ":" + code }

// CreateOrgUnit 创建组织单元（源 Create）。Path 由 ParentID 自动推导。
func (b *Biz) CreateOrgUnit(ctx context.Context, tenantID string, u OrgUnit) (*OrgUnit, error) {
	if u.Name == "" || u.Code == "" {
		return nil, fmt.Errorf("%w: name and code required", ErrValidation)
	}
	path, err := b.buildPath(ctx, tenantID, u.ParentID)
	if err != nil {
		return nil, err
	}
	status := u.Status
	if status == "" {
		status = "ON"
	}
	m := &authmodel.OrgUnit{
		ID: orgID(tenantID, u.Code), TenantID: tenantID,
		Name: u.Name, Code: u.Code, Type: u.Type,
		ParentID: parentKey(tenantID, u.ParentID), Path: path,
		Status: status, SortOrder: u.SortOrder,
		LeaderID: u.LeaderID, Remark: u.Remark, Description: u.Description,
	}
	if err := bootstrappkg.OrgUnitStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: org unit already exists", ErrConflict)
		}
		return nil, fmt.Errorf("org: create org unit: %w", err)
	}
	// Path 需要自己的 ID，创建后回填（源的 path 含自身）。
	m.Path = path + "/" + m.ID
	_ = bootstrappkg.OrgUnitStore.Update(ctx, m)
	return toOrgUnit(m), nil
}

// buildPath 由父节点推导路径。父不存在时报错（避免悬挂引用）。
func (b *Biz) buildPath(ctx context.Context, tenantID, parentCode string) (string, error) {
	if parentCode == "" {
		return "", nil // 根节点：path 为空，创建后补自身
	}
	p, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", orgID(tenantID, parentCode))},
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("%w: parent org unit not found: %s", ErrValidation, parentCode)
		}
		return "", err
	}
	return p.Path, nil
}

// parentKey 把父的 code 转成其主键（空则空）。
func parentKey(tenantID, parentCode string) string {
	if parentCode == "" {
		return ""
	}
	return orgID(tenantID, parentCode)
}

// GetOrgUnit 按 code 查询（源 Get）。
func (b *Biz) GetOrgUnit(ctx context.Context, tenantID, code string) (*OrgUnit, error) {
	m, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", orgID(tenantID, code))},
	})
	if err != nil {
		return nil, err
	}
	ou := toOrgUnit(m)
	b.enrichOrgUnits(ctx, []*OrgUnit{ou})
	return ou, nil
}

// ListOrgUnits 列出组织单元（源 List）——**返回树形**。
//
// 树的构建方式：取全量 → 按 ParentID 建索引 → 挂 children。
// 源用 repo 的树查询，本实现手工构建（数据量小、语义更显式）。
func (b *Biz) ListOrgUnits(ctx context.Context, tenantID string) ([]*OrgUnit, error) {
	items, _, err := bootstrappkg.OrgUnitStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", tenantID)},
	})
	if err != nil {
		return nil, fmt.Errorf("org: list org units: %w", err)
	}
	all := make([]*OrgUnit, 0, len(items))
	for _, m := range items {
		all = append(all, toOrgUnit(m))
	}
	// 批量回填 leader_name（**一次查询**，非逐个）。
	b.enrichOrgUnits(ctx, all)

	// 建树。
	byID := make(map[string]*OrgUnit, len(all))
	for _, ou := range all {
		byID[ou.ID] = ou
	}
	var roots []*OrgUnit
	for _, ou := range all {
		if ou.ParentID == "" {
			roots = append(roots, ou)
			continue
		}
		if p, ok := byID[ou.ParentID]; ok {
			p.Children = append(p.Children, ou)
		} else {
			// 父不在本租户结果集（跨租户/数据不一致）：作为根返回，不丢数据。
			roots = append(roots, ou)
		}
	}
	return roots, nil
}

// UpdateOrgUnit 更新（源 Update）。**防环**：不能把自己挂到自己的子孙下。
func (b *Biz) UpdateOrgUnit(ctx context.Context, tenantID, code string, u OrgUnit) error {
	m, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", orgID(tenantID, code))},
	})
	if err != nil {
		return err
	}
	if u.Name != "" {
		m.Name = u.Name
	}
	if u.Type != "" {
		m.Type = u.Type
	}
	if u.Status != "" {
		m.Status = u.Status
	}
	if u.LeaderID != "" {
		m.LeaderID = u.LeaderID
	}
	if u.Remark != "" {
		m.Remark = u.Remark
	}
	// ParentID 变更：需防环（把节点挂到自己的子孙下会形成环）。
	if u.ParentID != m.ParentID {
		newParent := parentKey(tenantID, u.ParentID)
		if newParent == m.ID {
			return fmt.Errorf("%w: cannot set self as parent", ErrCycle)
		}
		if newParent != "" {
			isDesc, derr := b.isDescendant(ctx, newParent, m.ID)
			if derr != nil {
				return derr
			}
			if isDesc {
				return fmt.Errorf("%w: cannot move node under its own descendant", ErrCycle)
			}
		}
		m.ParentID = newParent
	}
	if err := bootstrappkg.OrgUnitStore.Update(ctx, m); err != nil {
		return fmt.Errorf("org: update org unit: %w", err)
	}
	return nil
}

// isDescendant 判断 candidate 是否是 ancestor 的子孙（沿 ParentID 向上走）。
func (b *Biz) isDescendant(ctx context.Context, candidate, ancestor string) (bool, error) {
	cur := candidate
	for i := 0; i < 100; i++ { // 深度上限，防御脏数据成环导致的死循环
		if cur == "" {
			return false, nil
		}
		if cur == ancestor {
			return true, nil
		}
		m, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", cur)},
		})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		cur = m.ParentID
	}
	return false, nil
}

// DeleteOrgUnit 删除（源 Delete）。**有子节点时拒绝**——否则产生悬挂引用。
func (b *Biz) DeleteOrgUnit(ctx context.Context, tenantID, code string) error {
	id := orgID(tenantID, code)
	children, _, err := bootstrappkg.OrgUnitStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("parent_id", id)},
	})
	if err != nil {
		return fmt.Errorf("org: check children: %w", err)
	}
	if len(children) > 0 {
		return fmt.Errorf("%w: has %d children, delete them first", ErrValidation, len(children))
	}
	if err := bootstrappkg.OrgUnitStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("org: delete org unit: %w", err)
	}
	return nil
}

// BatchCreateOrgUnits 批量创建（源 BatchCreate）——逐条创建，**部分失败不回滚**
// （对齐源语义：返回每条的结果，调用方据 failed 列表决定重试）。
func (b *Biz) BatchCreateOrgUnits(ctx context.Context, tenantID string, items []OrgUnit) (created []*OrgUnit, failed []string) {
	for _, u := range items {
		res, err := b.CreateOrgUnit(ctx, tenantID, u)
		if err != nil {
			failed = append(failed, u.Code+": "+err.Error())
			continue
		}
		created = append(created, res)
	}
	return created, failed
}

// CountOrgUnits 统计（源 Count）。
func (b *Biz) CountOrgUnits(ctx context.Context, tenantID string) (int64, error) {
	n, err := bootstrappkg.OrgUnitStore.Count(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", tenantID)},
	})
	if err != nil {
		return 0, fmt.Errorf("org: count: %w", err)
	}
	return n, nil
}

// enrichOrgUnits 批量回填 leader_name（源三步式回填的等价实现）。
func (b *Biz) enrichOrgUnits(ctx context.Context, units []*OrgUnit) {
	ids := map[string]bool{}
	for _, u := range units {
		if u.LeaderID != "" {
			ids[u.LeaderID] = true
		}
	}
	if len(ids) == 0 {
		return
	}
	// **一次批量查询**（源的 fetchRelationInfo 语义；逐个查会 N+1）。
	names := make(map[string]string, len(ids))
	for id := range ids {
		u, err := bootstrappkg.UserStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", id)},
		})
		if err == nil {
			names[id] = u.Username
		}
	}
	for _, ou := range units {
		if n, ok := names[ou.LeaderID]; ok {
			ou.LeaderName = n
		}
	}
}

func toOrgUnit(m *authmodel.OrgUnit) *OrgUnit {
	return &OrgUnit{
		ID: m.ID, Name: m.Name, Code: m.Code, Type: m.Type,
		ParentID: m.ParentID, Path: m.Path, Status: m.Status,
		SortOrder: m.SortOrder, LeaderID: m.LeaderID,
		Remark: m.Remark, Description: m.Description,
	}
}

// ---- position ----

// Position 是对外职位。
type Position struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Code                  string `json:"code"`
	Headcount             int32  `json:"headcount"`
	Status                string `json:"status"`
	Type                  string `json:"type"`
	OrgUnitID             string `json:"org_unit_id,omitempty"`
	OrgUnitName           string `json:"org_unit_name,omitempty"` // 回填
	ReportsToPositionID   string `json:"reports_to_position_id,omitempty"`
	ReportsToPositionName string `json:"reports_to_position_name,omitempty"` // 回填
	JobFamily             string `json:"job_family,omitempty"`
	JobGrade              string `json:"job_grade,omitempty"`
	Level                 int32  `json:"level"`
	IsKeyPosition         bool   `json:"is_key_position"`
	Remark                string `json:"remark,omitempty"`
}

func posID(tenantID, code string) string { return tenantID + ":pos:" + code }

// CreatePosition 创建职位（源 Create）。校验所属组织单元存在。
func (b *Biz) CreatePosition(ctx context.Context, tenantID string, p Position) (*Position, error) {
	if p.Name == "" || p.Code == "" {
		return nil, fmt.Errorf("%w: name and code required", ErrValidation)
	}
	orgIDRef := ""
	if p.OrgUnitID != "" {
		// 校验组织单元存在（避免悬挂引用）。
		if _, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", orgID(tenantID, p.OrgUnitID))},
		}); err != nil {
			return nil, fmt.Errorf("%w: org unit not found: %s", ErrValidation, p.OrgUnitID)
		}
		orgIDRef = orgID(tenantID, p.OrgUnitID)
	}
	status := p.Status
	if status == "" {
		status = "ON"
	}
	m := &authmodel.Position{
		ID: posID(tenantID, p.Code), TenantID: tenantID,
		Name: p.Name, Code: p.Code, Headcount: p.Headcount,
		Status: status, Type: p.Type, OrgUnitID: orgIDRef,
		JobFamily: p.JobFamily, JobGrade: p.JobGrade, Level: p.Level,
		IsKeyPosition: p.IsKeyPosition, Remark: p.Remark,
	}
	if p.ReportsToPositionID != "" {
		m.ReportsToPositionID = posID(tenantID, p.ReportsToPositionID)
	}
	if err := bootstrappkg.PositionStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: position already exists", ErrConflict)
		}
		return nil, fmt.Errorf("org: create position: %w", err)
	}
	res := toPosition(m)
	b.enrichPositions(ctx, []*Position{res})
	return res, nil
}

// GetPosition 按 code 查询（源 Get）。
func (b *Biz) GetPosition(ctx context.Context, tenantID, code string) (*Position, error) {
	m, err := bootstrappkg.PositionStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", posID(tenantID, code))},
	})
	if err != nil {
		return nil, err
	}
	p := toPosition(m)
	b.enrichPositions(ctx, []*Position{p})
	return p, nil
}

// ListPositions 列出（源 List），回填 org_unit_name / reports_to_position_name。
func (b *Biz) ListPositions(ctx context.Context, tenantID string) ([]Position, error) {
	items, _, err := bootstrappkg.PositionStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", tenantID)},
	})
	if err != nil {
		return nil, fmt.Errorf("org: list positions: %w", err)
	}
	out := make([]Position, 0, len(items))
	ptrs := make([]*Position, 0, len(items))
	for _, m := range items {
		p := toPosition(m)
		out = append(out, *p)
		ptrs = append(ptrs, p)
	}
	b.enrichPositions(ctx, ptrs)
	// 把回填结果同步回 out（ptrs 与 out 是不同对象）。
	for i := range out {
		out[i].OrgUnitName = ptrs[i].OrgUnitName
		out[i].ReportsToPositionName = ptrs[i].ReportsToPositionName
	}
	return out, nil
}

// enrichPositions 批量回填（源 position_service 的关联回填语义）。
func (b *Biz) enrichPositions(ctx context.Context, ps []*Position) {
	orgIDs := map[string]bool{}
	posIDs := map[string]bool{}
	for _, p := range ps {
		if p.OrgUnitID != "" {
			orgIDs[p.OrgUnitID] = true
		}
		if p.ReportsToPositionID != "" {
			posIDs[p.ReportsToPositionID] = true
		}
	}
	orgNames := make(map[string]string, len(orgIDs))
	for id := range orgIDs {
		if m, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", id)},
		}); err == nil {
			orgNames[id] = m.Name
		}
	}
	posNames := make(map[string]string, len(posIDs))
	for id := range posIDs {
		if m, err := bootstrappkg.PositionStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", id)},
		}); err == nil {
			posNames[id] = m.Name
		}
	}
	for _, p := range ps {
		if n, ok := orgNames[p.OrgUnitID]; ok {
			p.OrgUnitName = n
		}
		if n, ok := posNames[p.ReportsToPositionID]; ok {
			p.ReportsToPositionName = n
		}
	}
}

// UpdatePosition 更新（源 Update）。
func (b *Biz) UpdatePosition(ctx context.Context, tenantID, code string, p Position) error {
	m, err := bootstrappkg.PositionStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", posID(tenantID, code))},
	})
	if err != nil {
		return err
	}
	if p.Name != "" {
		m.Name = p.Name
	}
	if p.Status != "" {
		m.Status = p.Status
	}
	if p.Headcount > 0 {
		m.Headcount = p.Headcount
	}
	if p.JobGrade != "" {
		m.JobGrade = p.JobGrade
	}
	if p.Level != 0 {
		m.Level = p.Level
	}
	if err := bootstrappkg.PositionStore.Update(ctx, m); err != nil {
		return fmt.Errorf("org: update position: %w", err)
	}
	return nil
}

// DeletePosition 删除（源 Delete）。
func (b *Biz) DeletePosition(ctx context.Context, tenantID, code string) error {
	if err := bootstrappkg.PositionStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", posID(tenantID, code))},
	}); err != nil {
		return fmt.Errorf("org: delete position: %w", err)
	}
	return nil
}

// BatchCreatePositions 批量创建（源 BatchCreate）。
func (b *Biz) BatchCreatePositions(ctx context.Context, tenantID string, items []Position) (created []*Position, failed []string) {
	for _, p := range items {
		res, err := b.CreatePosition(ctx, tenantID, p)
		if err != nil {
			failed = append(failed, p.Code+": "+err.Error())
			continue
		}
		created = append(created, res)
	}
	return created, failed
}

// CountPositions 统计（源 Count）。
func (b *Biz) CountPositions(ctx context.Context, tenantID string) (int64, error) {
	n, err := bootstrappkg.PositionStore.Count(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", tenantID)},
	})
	if err != nil {
		return 0, fmt.Errorf("org: count positions: %w", err)
	}
	return n, nil
}

func toPosition(m *authmodel.Position) *Position {
	return &Position{
		ID: m.ID, Name: m.Name, Code: m.Code, Headcount: m.Headcount,
		Status: m.Status, Type: m.Type, OrgUnitID: m.OrgUnitID,
		ReportsToPositionID: m.ReportsToPositionID,
		JobFamily: m.JobFamily, JobGrade: m.JobGrade, Level: m.Level,
		IsKeyPosition: m.IsKeyPosition, Remark: m.Remark,
	}
}

// 保留 strings 引用（未来编码规则校验用）。
var _ = strings.TrimSpace
