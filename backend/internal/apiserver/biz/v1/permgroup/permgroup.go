// Package permgroup 实现权限组与策略评估日志域（Wave 4.1，源 7 rpc）。
//
// 源：
//   - `i_permission_group.proto`（5 rpc：List/Get/Create/Update/Delete）
//   - `i_policy_evaluation_log.proto`（2 rpc：List/Get）
//
// ## 权限组的树形语义（本域核心）
//
// 源用**物化路径**：`Path` 格式 `/1/10/101/`——**含自身、首尾带 `/`**
// （`permission_group.proto:53-56` 的 description 原文：
// 「树形路径，格式：/1/10/101/（包含自身且首尾带/）」）。
//
// 创建时序（与源 `setTreePath` 一致）：节点 ID 由 DB 生成，
// 而 path 要含自身 ID → **先落库、再回填 path**（本项目 org 域同款，
// 见 `biz/v1/org/org.go:92-95` 注释「Path 需要自己的 ID，创建后回填」）。
//
// 与本项目 org 域的差异：org 的 path 是 `/root/child`（无尾 `/`），
// 本域**对齐源**用 `/1/10/101/`（首尾带 `/`）——复刻任务以源为准。
package permgroup

import (
	"context"
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
var ErrValidation = errors.New("permgroup: validation failed")

// ErrNotFound 资源不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 资源已存在。
var ErrConflict = store.ErrConflict

// Biz 是权限组 + 策略评估日志业务。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// Group 是对外的权限组。
type Group struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	Module    string  `json:"module,omitempty"`
	ParentID  string  `json:"parent_id,omitempty"`
	Status    string  `json:"status"`
	SortOrder int32   `json:"sort_order"`
	Remark    string  `json:"remark,omitempty"`
	// Children 树形返回时的子节点（源 ListPermissionGroupResponse 同此）。
	Children []*Group `json:"children,omitempty"`
}

// EvalLog 是对外的策略评估日志。
type EvalLog struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id,omitempty"`
	PermissionID  string `json:"permission_id,omitempty"`
	PolicyID      string `json:"policy_id,omitempty"`
	RequestPath   string `json:"request_path,omitempty"`
	RequestMethod string `json:"request_method,omitempty"`
	Result        bool   `json:"result"`
	EffectDetails string `json:"effect_details,omitempty"`
	CreatedAt     int64  `json:"created_at,omitempty"`
}

func pgID(tenantID, code string) string { return tenantID + ":pg:" + code }

// ---- 权限组 ----

// CreateGroup 创建权限组（源 Create）。
//
// parentID 为空即根节点；非空时父必须存在（避免悬挂引用）。
// path 在落库后回填（含自身）。
func (b *Biz) CreateGroup(ctx context.Context, tenantID, code string, g Group) (*Group, error) {
	if g.Name == "" {
		return nil, fmt.Errorf("%w: name required", ErrValidation)
	}
	if code == "" {
		return nil, fmt.Errorf("%w: code required", ErrValidation)
	}

	// 1) 解析父路径（源 setTreePath 语义）。
	parentPath := ""
	if g.ParentID != "" {
		p, err := bootstrappkg.PermGroupStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("id", g.ParentID)},
		})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: parent group not found: %s", ErrValidation, g.ParentID)
			}
			return nil, err
		}
		parentPath = p.Path
	}

	status := g.Status
	if status == "" {
		status = "ON" // 源枚举 ON=1 为默认启用
	}

	m := &authmodel.PermissionGroup{
		ID:       pgID(tenantID, code),
		TenantID: tenantID,
		Name:     g.Name,
		Module:   g.Module,
		ParentID: g.ParentID,
		Status:   status,
		SortOrder: g.SortOrder,
		Remark:   g.Remark,
	}
	// 2) 先落库（path 暂空）。
	if err := bootstrappkg.PermGroupStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: permission group already exists: %s", ErrConflict, code)
		}
		return nil, fmt.Errorf("permgroup: create: %w", err)
	}
	// 3) 回填 path —— 格式 `/父path/自身ID/`，含自身、首尾带 `/`（对齐源）。
	m.Path = computePath(parentPath, m.ID)
	if err := bootstrappkg.PermGroupStore.Update(ctx, m); err != nil {
		return nil, fmt.Errorf("permgroup: backfill path: %w", err)
	}
	return toGroup(m), nil
}

// computePath 按源的格式生成物化路径：`/父段/自身/`。
//
// 源格式（`permission_group.proto:53-56`）：「格式：/1/10/101/
// （包含自身且首尾带/）」——故：
//
//	根节点        → "/<self>/"
//	有父（父为 /1/）→ "/1/<self>/"
//
// 注意父 path 本身已带尾 `/`，故拼接时只需补自身段与尾 `/`。
func computePath(parentPath, selfID string) string {
	// selfID 形如 "<tenant>:pg:<code>"，路径段取 code 部分（源用数字 ID，
	// 本项目用 code——语义等价：路径段是节点的稳定标识）。
	seg := selfID
	if i := strings.LastIndex(selfID, ":"); i >= 0 {
		seg = selfID[i+1:]
	}
	if parentPath == "" {
		return "/" + seg + "/"
	}
	// parentPath 已是 "/.../" 形式，直接续接。
	return parentPath + seg + "/"
}

// GetGroup 按 id 查询（源 Get）。
func (b *Biz) GetGroup(ctx context.Context, id string) (*Group, error) {
	m, err := bootstrappkg.PermGroupStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toGroup(m), nil
}

// ListGroupTree 返回树形权限组（源 List，按 path 组装父子）。
func (b *Biz) ListGroupTree(ctx context.Context, tenantID string) ([]*Group, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.PermGroupStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("permgroup: list: %w", err)
	}

	// 先转成 Group 并建索引。
	byID := make(map[string]*Group, len(items))
	for _, m := range items {
		byID[m.ID] = toGroup(m)
	}
	// 再挂父子（按 ParentID）。
	roots := make([]*Group, 0)
	for _, m := range items {
		g := byID[m.ID]
		if m.ParentID == "" {
			roots = append(roots, g)
			continue
		}
		if parent, ok := byID[m.ParentID]; ok {
			parent.Children = append(parent.Children, g)
		} else {
			// 父不存在（理论上不该发生）：当作根，避免节点丢失。
			roots = append(roots, g)
		}
	}
	return roots, nil
}

// UpdateGroup 更新（源 Update）。
func (b *Biz) UpdateGroup(ctx context.Context, id string, g Group) error {
	m, err := bootstrappkg.PermGroupStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return err
	}
	if g.Name != "" {
		m.Name = g.Name
	}
	if g.Module != "" {
		m.Module = g.Module
	}
	if g.Status != "" {
		m.Status = g.Status
	}
	m.SortOrder = g.SortOrder
	if g.Remark != "" {
		m.Remark = g.Remark
	}
	if err := bootstrappkg.PermGroupStore.Update(ctx, m); err != nil {
		return fmt.Errorf("permgroup: update: %w", err)
	}
	return nil
}

// DeleteGroup 删除（源 Delete）。
//
// **有子节点时拒绝**（与本项目 org 域同语义，`org.go` 的
// 「has N children, delete them first」）——避免留下悬挂的父子引用。
func (b *Biz) DeleteGroup(ctx context.Context, id string) error {
	children, _, err := bootstrappkg.PermGroupStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("parent_id", id)},
	})
	if err != nil {
		return fmt.Errorf("permgroup: check children: %w", err)
	}
	if len(children) > 0 {
		return fmt.Errorf("%w: has %d children, delete them first", ErrValidation, len(children))
	}
	if err := bootstrappkg.PermGroupStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("permgroup: delete: %w", err)
	}
	return nil
}

func toGroup(m *authmodel.PermissionGroup) *Group {
	return &Group{
		ID: m.ID, Name: m.Name, Path: m.Path, Module: m.Module,
		ParentID: m.ParentID, Status: m.Status, SortOrder: m.SortOrder,
		Remark: m.Remark,
	}
}

// ---- 策略评估日志 ----

// RecordEvaluation 落一条策略评估日志。
//
// **本域是「读为主」**（源只有 List/Get）——写入由权限判定链路调用。
// 本项目当前未在 authz 中间件里接线（那是 Wave 4 后续或独立改动），
// 故此处提供写入能力供测试与后续接线使用。
func (b *Biz) RecordEvaluation(ctx context.Context, tenantID, userID, permissionID,
	requestPath, requestMethod string, allowed bool, details string) error {
	now := time.Now()
	rec := &authmodel.PolicyEvaluationLog{
		ID:            fmt.Sprintf("%s:pel:%d", tenantID, now.UnixNano()),
		TenantID:      tenantID,
		UserID:        userID,
		PermissionID:  permissionID,
		RequestPath:   requestPath,
		RequestMethod: requestMethod,
		Result:        allowed,
		EffectDetails: details,
		CreatedAt:     now.UnixNano(),
	}
	if err := bootstrappkg.PolicyEvalLogStore.Create(ctx, rec); err != nil {
		return fmt.Errorf("permgroup: record evaluation: %w", err)
	}
	return nil
}

// ListEvalLogs 列出策略评估日志（源 List）。
func (b *Biz) ListEvalLogs(ctx context.Context, tenantID string) ([]EvalLog, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.PolicyEvalLogStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("permgroup: list eval logs: %w", err)
	}
	out := make([]EvalLog, 0, len(items))
	for _, m := range items {
		out = append(out, *toEvalLog(m))
	}
	return out, nil
}

// GetEvalLog 按 id 查询（源 Get）。
func (b *Biz) GetEvalLog(ctx context.Context, id string) (*EvalLog, error) {
	m, err := bootstrappkg.PolicyEvalLogStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toEvalLog(m), nil
}

func toEvalLog(m *authmodel.PolicyEvaluationLog) *EvalLog {
	return &EvalLog{
		ID: m.ID, UserID: m.UserID, PermissionID: m.PermissionID,
		PolicyID: m.PolicyID, RequestPath: m.RequestPath,
		RequestMethod: m.RequestMethod, Result: m.Result,
		EffectDetails: m.EffectDetails, CreatedAt: m.CreatedAt,
	}
}
