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
	"time"

	"github.com/kalandramo/bald/berrors"
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

// badRequest 构造带 reason 的 400（writeBizErr 只识别 *berrors.Error；
// 裸 error 会被兜底成 500——见 language.go 的实测踩坑记录）。
// reason 供前端/details 判别具体校验失败类型。
func badRequest(reason, format string, args ...any) error {
	return berrors.BadRequest(reason).WithMessage(format, args...)
}

// notFound 构造 404。
func notFound(reason, format string, args ...any) error {
	return berrors.NotFound(reason).WithMessage(format, args...)
}

// conflict 构造 409。
func conflict(reason, format string, args ...any) error {
	return berrors.AlreadyExists(reason).WithMessage(format, args...)
}

// Biz 是组织架构业务。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// ---- org_unit ----

// OrgUnit 是对外的组织单元（含 children，树形）。
//
// **无 json tag**（A 轨范式）：handler 用 bindPB/writePB 走 protojson，
// 序列化形状由 proto 契约决定，DTO 不再承担 JSON 映射职责。
type OrgUnit struct {
	ID          string
	Name        string
	Code        string
	Type        string
	ParentID    string // 父节点 **code**（空 = 根）
	Path        string
	Status      string
	SortOrder   int32
	LeaderID    string
	LeaderName  string // 回填
	Remark      string
	Description string
	Children    []*OrgUnit // 树形
}

func orgID(tenantID, code string) string { return tenantID + ":" + code }

// withParentFilter 给分页请求附加「parent_id = key」过滤（保留调用方已有条件）。
//
// 为什么走 filter_expr 而非外部 Filters：`Store.ListWithPaging` 的 translate
// （bald/pkg/store/store.go）只从 PagingRequest 构造 where（Sorting + FilterExpr），
// **不接受外部附加的 Filters**——故业务过滤必须编码进 req.FilterExpr。
//
// 两个易错点：
//   - filter_expr 是 PagingRequest 的 **oneof** 字段，必须用
//     `&storev1.PagingRequest_FilterExpr{...}` 包装，直接赋 req.FilterExpr 编译不过；
//   - 框架已有条件构造辅助 store.Eq，不必手写 FilterCondition。
//
// key 语义：空串 = 根节点（model.ParentID 存空串）；非空 = 父节点**主键**
// （`orgID(tenant, code)`，见 parentKey——model 存主键而非 code）。
//
// 租户隔离由 Store.translate 的 mergeTenant 自动注入（bootstrap 已
// RegisterTenant("tenant_id", ...)），走 where.Filters 通道，与本函数的
// where.Expr 通道按 AND 组合，互不覆盖。
func withParentFilter(req *storev1.PagingRequest, parentKey string) *storev1.PagingRequest {
	if req == nil {
		req = &storev1.PagingRequest{}
	}
	expr := &storev1.FilterExpr{
		Type:       storev1.ExprType_AND,
		Conditions: []*storev1.FilterCondition{store.Eq("parent_id", parentKey)},
	}
	if prev := req.GetFilterExpr(); prev != nil {
		// 与调用方条件按 AND 合并（避免覆盖前端传入的过滤）。
		expr.Groups = []*storev1.FilterExpr{prev}
	}
	req.FilteringType = &storev1.PagingRequest_FilterExpr{FilterExpr: expr}
	return req
}

// CreateOrgUnit 创建组织单元（源 Create）。Path 由 ParentID 自动推导。
func (b *Biz) CreateOrgUnit(ctx context.Context, tenantID string, u OrgUnit) (*OrgUnit, error) {
	if u.Name == "" || u.Code == "" {
		return nil, badRequest("org/invalid_request", "name and code required")
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
			return nil, conflict("org/conflict", "org unit already exists")
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
			return "", badRequest("org/invalid_request", "parent org unit not found: %s", parentCode)
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

// ListOrgUnits 列出**根节点**（分页，children 不预填）。
//
// 与旧实现（全量 + 手工建树）的差异：树形改由前端 el-table 懒加载组装——
// 首屏只取根节点分页，展开节点时调 ListOrgUnitChildren。这样避免了
// 「分页截断 children 导致树不完整」的语义冲突。
//
// 租户隔离由 Store.translate 自动注入（无需手工 Eq("tenant_id", ...)）。
func (b *Biz) ListOrgUnits(ctx context.Context,
	req *storev1.PagingRequest) ([]*OrgUnit, *storev1.PaginationResponseMeta, error) {
	// 根节点 = parent_id 为空串（见 withParentFilter 的 key 语义）。
	result, err := bootstrappkg.OrgUnitStore.ListWithPaging(ctx, withParentFilter(req, ""))
	if err != nil {
		return nil, nil, fmt.Errorf("org: list org units: %w", err)
	}
	out := make([]*OrgUnit, 0, len(result.Items))
	for _, m := range result.Items {
		out = append(out, toOrgUnit(m))
	}
	// 批量回填 leader_name（**一次查询**，非逐个）。
	b.enrichOrgUnits(ctx, out)
	return out, result.Meta, nil
}

// ListOrgUnitChildren 列出某节点的**直接子节点**（分页，不递归）。
//
// parentCode 是父节点 **code**（对外语义）；model 的 parent_id 存的是**主键**，
// 故须经 orgID 转码后再过滤（见 withParentFilter 的 key 语义）。
func (b *Biz) ListOrgUnitChildren(ctx context.Context, tenantID, parentCode string,
	req *storev1.PagingRequest) ([]*OrgUnit, *storev1.PaginationResponseMeta, error) {
	if parentCode == "" {
		return nil, nil, badRequest("org/invalid_request", "parent_id required")
	}
	result, err := bootstrappkg.OrgUnitStore.ListWithPaging(ctx,
		withParentFilter(req, orgID(tenantID, parentCode)))
	if err != nil {
		return nil, nil, fmt.Errorf("org: list children: %w", err)
	}
	out := make([]*OrgUnit, 0, len(result.Items))
	for _, m := range result.Items {
		out = append(out, toOrgUnit(m))
	}
	b.enrichOrgUnits(ctx, out)
	return out, result.Meta, nil
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
	//
	// **空 = 不改**（与其余字段的「非空即更新」语义对齐）。此前用
	// `u.ParentID != m.ParentID` 比较——两侧语义不同（code vs 完整主键）恒不等，
	// 导致每次更新都进此分支；若前端未传 parent_id 会**清空父节点**（数据损坏）。
	// 代价：本波不支持「把节点移为根节点」（需显式清空），记为后续迭代。
	if u.ParentID != "" {
		newParent := parentKey(tenantID, u.ParentID)
		if newParent != m.ParentID {
			if newParent == m.ID {
				return badRequest("org/cycle", "cannot set self as parent")
			}
			isDesc, derr := b.isDescendant(ctx, newParent, m.ID)
			if derr != nil {
				return derr
			}
			if isDesc {
				return badRequest("org/cycle", "cannot move node under its own descendant")
			}
			m.ParentID = newParent
		}
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
		return badRequest("org/has_children", "has %d children, delete them first", len(children))
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

// toOrgUnit 模型 → DTO。
//
// ParentID 语义统一（源缺陷修复）：model 存**完整主键** `"<tenant>:<code>"`，
// 但对外契约（proto）用 **code**——此前直接输出主键导致「输入 code、输出主键」
// 的语义不对称（前端表单填 code，回显却是主键）。
func toOrgUnit(m *authmodel.OrgUnit) *OrgUnit {
	return &OrgUnit{
		ID: m.ID, Name: m.Name, Code: m.Code, Type: m.Type,
		ParentID: codeFromKey(m.ParentID), Path: m.Path, Status: m.Status,
		SortOrder: m.SortOrder, LeaderID: m.LeaderID,
		Remark: m.Remark, Description: m.Description,
	}
}

// codeFromKey 从完整主键 `"<tenant>:<code>"` 取回 code（空键返回空）。
// 注意 Position 主键含 `:pos:` 中缀（`"<tenant>:pos:<code>"`），故先剥中缀。
func codeFromKey(key string) string {
	if key == "" {
		return ""
	}
	// 去掉 "<tenant>:" 前缀。
	if i := strings.Index(key, ":"); i >= 0 {
		rest := key[i+1:]
		return strings.TrimPrefix(rest, "pos:")
	}
	return key
}

// ---- position ----

// Position 是对外职位（无 json tag，同 OrgUnit）。
type Position struct {
	ID                    string
	Name                  string
	Code                  string
	Headcount             int32
	Status                string
	Type                  string
	OrgUnitID             string // 所属组织单元 **code**
	OrgUnitName           string // 回填
	ReportsToPositionID   string // 汇报上级职位 **code**
	ReportsToPositionName string // 回填
	JobFamily             string
	JobGrade              string
	Level                 int32
	SortOrder             int32 // O2 修复：接入 model 的 SortOrder（此前全链路零写入）
	IsKeyPosition         bool
	Remark                string
	Description           string
	StartAt               *time.Time
}

func posID(tenantID, code string) string { return tenantID + ":pos:" + code }

// CreatePosition 创建职位（源 Create）。校验所属组织单元存在。
func (b *Biz) CreatePosition(ctx context.Context, tenantID string, p Position) (*Position, error) {
	if p.Name == "" || p.Code == "" {
		return nil, badRequest("org/invalid_request", "name and code required")
	}
	orgIDRef := ""
	if p.OrgUnitID != "" {
		// 校验组织单元存在（避免悬挂引用）。
		if _, err := bootstrappkg.OrgUnitStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", orgID(tenantID, p.OrgUnitID))},
		}); err != nil {
			return nil, badRequest("org/invalid_request", "org unit not found: %s", p.OrgUnitID)
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
		SortOrder: p.SortOrder, // O2 修复：接入（此前全链路零写入）
		IsKeyPosition: p.IsKeyPosition, Remark: p.Remark,
		Description: p.Description, StartAt: p.StartAt,
	}
	if p.ReportsToPositionID != "" {
		m.ReportsToPositionID = posID(tenantID, p.ReportsToPositionID)
	}
	if err := bootstrappkg.PositionStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, conflict("org/conflict", "position already exists")
		}
		return nil, fmt.Errorf("org: create position: %w", err)
	}
	res := toPosition(m)
	b.enrichPositions(ctx, tenantID, []*Position{res})
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
	b.enrichPositions(ctx, tenantID, []*Position{p})
	return p, nil
}

// ListPositions 列出（源 List），回填 org_unit_name / reports_to_position_name。
//
// O3 修复：返回 `[]*Position`（此前是 `[]Position` 值切片，与 ListOrgUnits 的
// `[]*OrgUnit` 不对称，且需手工把回填结果同步回值切片——纯属冗余）。
func (b *Biz) ListPositions(ctx context.Context, tenantID string) ([]*Position, error) {
	items, _, err := bootstrappkg.PositionStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("tenant_id", tenantID)},
	})
	if err != nil {
		return nil, fmt.Errorf("org: list positions: %w", err)
	}
	out := make([]*Position, 0, len(items))
	for _, m := range items {
		out = append(out, toPosition(m))
	}
	b.enrichPositions(ctx, tenantID, out)
	return out, nil
}

// enrichPositions 批量回填（源 position_service 的关联回填语义）。
//
// **注意**：DTO 的 OrgUnitID / ReportsToPositionID 是 **code**（对外契约），
// 而 store 的主键是完整键 `"<tenant>:<code>"`——此处需转换后再查。
func (b *Biz) enrichPositions(ctx context.Context, tenantID string, ps []*Position) {
	orgIDs := map[string]bool{}
	posIDs := map[string]bool{}
	for _, p := range ps {
		if p.OrgUnitID != "" {
			orgIDs[orgID(tenantID, p.OrgUnitID)] = true
		}
		if p.ReportsToPositionID != "" {
			posIDs[posID(tenantID, p.ReportsToPositionID)] = true
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
		if n, ok := orgNames[orgID(tenantID, p.OrgUnitID)]; ok {
			p.OrgUnitName = n
		}
		if n, ok := posNames[posID(tenantID, p.ReportsToPositionID)]; ok {
			p.ReportsToPositionName = n
		}
	}
}

// UpdatePosition 更新（源 Update）。「非空即更新」语义（同 UpdateOrgUnit）。
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
	if p.Type != "" {
		m.Type = p.Type
	}
	if p.Headcount > 0 {
		m.Headcount = p.Headcount
	}
	if p.SortOrder > 0 { // O2 修复：接入
		m.SortOrder = p.SortOrder
	}
	if p.JobFamily != "" {
		m.JobFamily = p.JobFamily
	}
	if p.JobGrade != "" {
		m.JobGrade = p.JobGrade
	}
	if p.Level != 0 {
		m.Level = p.Level
	}
	if p.Remark != "" {
		m.Remark = p.Remark
	}
	if p.Description != "" {
		m.Description = p.Description
	}
	if p.OrgUnitID != "" {
		m.OrgUnitID = orgID(tenantID, p.OrgUnitID)
	}
	if p.ReportsToPositionID != "" {
		m.ReportsToPositionID = posID(tenantID, p.ReportsToPositionID)
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
	p := &Position{
		ID: m.ID, Name: m.Name, Code: m.Code, Headcount: m.Headcount,
		Status: m.Status, Type: m.Type,
		OrgUnitID:           codeFromKey(m.OrgUnitID),
		ReportsToPositionID: codeFromKey(m.ReportsToPositionID),
		JobFamily:           m.JobFamily, JobGrade: m.JobGrade, Level: m.Level,
		SortOrder:     m.SortOrder, // O2 修复：接入
		IsKeyPosition: m.IsKeyPosition, Remark: m.Remark,
		Description: m.Description, StartAt: m.StartAt,
	}
	return p
}

// 保留 strings 引用（未来编码规则校验用）。
var _ = strings.TrimSpace
