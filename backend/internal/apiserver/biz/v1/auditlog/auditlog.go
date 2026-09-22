package auditlog

// Package auditlog 审计日志查询业务层（T6）。只读：数据由写路径（拦截器链/
// 登录动作）经 StoreAuditor 自动落库，本层仅提供过滤+分页检索。
//
// **租户隔离（2026-09-22 变更）**：本域原为「跨租户全量查询」（Where 不注入
// 租户条件，理由是「审计是跨租户留痕」）。统一分页风格时改用
// `Store.ListWithPaging`——它经 translate → mergeTenant **强制注入租户隔离**，
// 框架无跳过机制。故现为「仅本租户可见」，admin 不再能查到其他租户的审计。
// 用户决策：接受隔离（多租户下「全量跨租户」是潜在信息泄露面）。
//
// store 经请求期包级引用（bootstrap.AuditStore，InitBridges 装配后可用）。

import (
	"context"

	"github.com/kalandramo/bald/berrors"
	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// 分页边界：默认页大小与上限由 `AuditStore` 的 WithPageSize/WithMaxPageSize
// 配置（见 bootstrap.go）——2026-09-22 统一分页风格后，本层不再自持常量。

// ListFilter 审计查询过滤条件（全可选；非空即过滤）。
type ListFilter struct {
	Category string // operation / login
	Subject  string
	Object   string
	Action   string
	Result   string // allow / deny / error
	IP       string // 客户端 IP
}

// ListResult 查询结果：当前页条目 + 分页元数据。
//
// Meta 直接携带框架的 PaginationResponseMeta（含 total / total_pages /
// next_token / page_size 等），handler 透传给 proto 响应——避免在 biz 层
// 拆解再在 handler 重组（旧实现拆成 Total/NextPageToken 两个裸字段，
// 丢失了 total_pages 等信息）。
type ListResult struct {
	Items []*authmodel.AuditRecord
	Meta  *storev1.PaginationResponseMeta
}

// Biz 审计查询业务。
type Biz struct{}

// New 构造审计查询 biz。
func New() *Biz { return &Biz{} }

// auditStore 请求期读取（InitBridges 装配后可用）。
func (b *Biz) auditStore() *store.Store[authmodel.AuditRecord] {
	return bootstrappkg.AuditStore
}

// List 分页查询审计日志（框架标准分页，2026-09-22 统一）。
//
// 与旧实现（手工 store.Where + Offset/Limit + 自研 token 编解码）的差异：
//   - 用 `Store.ListWithPaging` 替代手工分页——获得框架的 token 游标、
//     total_pages、NoPaging 等能力；
//   - 六个过滤条件编码进 `filter_expr`（translate 不接受外部 Filters）；
//   - **租户隔离由 mergeTenant 自动注入**——这是一处行为变更：本域原为
//     「跨租户全量查询」（Where 不注入 T），现改为「仅本租户可见」。
//     框架无跳过隔离的机制，且用户已确认接受（多租户下消除信息泄露面）。
func (b *Biz) List(ctx context.Context, f ListFilter,
	req *storev1.PagingRequest) (*ListResult, error) {
	if req == nil {
		req = &storev1.PagingRequest{}
	}

	// 六个过滤条件 → filter_expr 布尔树（AND 组合）。
	conds := make([]*storev1.FilterCondition, 0, 6)
	if f.Category != "" {
		conds = append(conds, store.Eq("category", f.Category))
	}
	if f.Subject != "" {
		conds = append(conds, store.Eq("subject", f.Subject))
	}
	if f.Object != "" {
		conds = append(conds, store.Eq("object", f.Object))
	}
	if f.Action != "" {
		conds = append(conds, store.Eq("action", f.Action))
	}
	if f.Result != "" {
		conds = append(conds, store.Eq("result", f.Result))
	}
	if f.IP != "" {
		conds = append(conds, store.Eq("ip_address", f.IP))
	}

	// 与调用方已有条件按 AND 合并（保留调用方传入的过滤）。
	var groups []*storev1.FilterExpr
	if prev := req.GetFilterExpr(); prev != nil {
		groups = append(groups, prev)
	}
	// filter_expr 是 PagingRequest 的 **oneof** 字段，必须用
	// PagingRequest_FilterExpr 包装（直接赋 req.FilterExpr 编译不过）。
	req.FilteringType = &storev1.PagingRequest_FilterExpr{
		FilterExpr: &storev1.FilterExpr{
			Type:       storev1.ExprType_AND,
			Conditions: conds,
			Groups:     groups,
		},
	}

	// 排序：时间降序（最新在前，审计查询惯例）。store.SortDesc 见
	// bald/pkg/store/where.go:103。
	if len(req.GetSorting()) == 0 {
		req.Sorting = []*storev1.Sorting{store.SortDesc("time")}
	}

	result, err := b.auditStore().ListWithPaging(ctx, req)
	if err != nil {
		return nil, err
	}
	return &ListResult{Items: result.Items, Meta: result.Meta}, nil
}

// Get 取单条审计日志。
func (b *Biz) Get(ctx context.Context, id string) (*authmodel.AuditRecord, error) {
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", id))
	m, err := b.auditStore().Get(ctx, w)
	if err != nil || m == nil {
		return nil, berrors.NotFound("audit/not_found")
	}
	return m, nil
}
