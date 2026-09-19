// Package dashboard 实现后台首页分析域的只读聚合统计（Wave 3.4，源 4 rpc）。
//
// 源实现：`go-wind-admin/backend/app/admin/service/internal/service/dashboard_service.go`
// + `data/dashboard_repo.go`。4 条 rpc：
//
//   - GetOverview                  四张概览卡计数（用户/角色/今日登录/今日操作）
//   - GetLoginTrend                近 N 天每日登录次数（**按日期升序、缺日补零**）
//   - GetOperationActionDistribution  操作审计按 action 分布
//   - GetLoginStatusDistribution   登录审计按 result 分布
//
// ## 数据来源（**不新建表**）
//
// 本项目已有 `AuditRecord`（`model/audit.go`），其 `Category` 字段区分
// `"login"`（登录动作）与 `"operation"`（拦截器链）——**恰好覆盖源的
// LoginAuditLog / OperationAuditLog 双表语义**（源用双表，本项目单表 + Category）。
// 故本域是**纯读聚合**，零新增存储。
//
// ## 多租户语义（**注意差异**）
//
// `AuditRecord` 的 TenantID 是「仅作列存储，不走 pkg/store 自动过滤」
// （`model/audit.go` 注释载明：审计是写全量留痕，读隔离由查询方按需施加）。
// 故本域**显式按 tenantID 过滤**——与 User/Role 的自动过滤不同，
// 不能依赖框架的 TenantPrivacy 机制。
package dashboard

import (
	"context"
	"fmt"
	"time"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// Biz 是 dashboard 业务（只读聚合）。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// Overview 是概览卡数据（源 DashboardOverviewResponse）。
type Overview struct {
	UserCount           int64 `json:"user_count"`
	RoleCount           int64 `json:"role_count"`
	TodayLoginCount     int64 `json:"today_login_count"`
	TodayOperationCount int64 `json:"today_operation_count"`
}

// TrendPoint 是趋势图上的一个点（源 TrendPoint）。
type TrendPoint struct {
	Date  string `json:"date"` // "2006-01-02"
	Count int64  `json:"count"`
}

// DistributionItem 是分布图的一项（源 DistributionItem）。
type DistributionItem struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// dayStart 返回 t 所在自然日的 0 点（本地时区）。
//
// 源用 `time.Local`（`dashboard_repo.go` 的 `loc := time.Local`）——
// 「今日」按服务器本地时区切分，而非 UTC。
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// GetOverview 返回四张概览卡的计数（源 GetOverview）。
func (b *Biz) GetOverview(ctx context.Context, tenantID string) (*Overview, error) {
	// 1) 活跃用户数（源 CountActiveUsers）。
	userWhere := &store.Where{}
	if tenantID != "" {
		userWhere.Filters = append(userWhere.Filters, store.Eq("tenant_id", tenantID))
	}
	userCount, err := bootstrappkg.UserStore.Count(ctx, userWhere)
	if err != nil {
		return nil, fmt.Errorf("dashboard: count users: %w", err)
	}

	// 2) 角色数（源 CountRoles）。角色是全局的（无 TenantID 字段）。
	roleCount, err := bootstrappkg.RoleStore.Count(ctx, &store.Where{})
	if err != nil {
		return nil, fmt.Errorf("dashboard: count roles: %w", err)
	}

	// 3) 今日登录数 / 4) 今日操作数——同一次审计扫描算两个数。
	now := time.Now()
	start := dayStart(now).UnixNano()
	end := dayStart(now.AddDate(0, 0, 1)).UnixNano()

	todayLogin, todayOp, err := b.countTodayByCategory(ctx, tenantID, start, end)
	if err != nil {
		return nil, err
	}

	return &Overview{
		UserCount:           userCount,
		RoleCount:           roleCount,
		TodayLoginCount:     todayLogin,
		TodayOperationCount: todayOp,
	}, nil
}

// countTodayByCategory 统计 [start,end) 内 login / operation 两类审计数。
func (b *Biz) countTodayByCategory(ctx context.Context, tenantID string, start, end int64) (login, operation int64, err error) {
	records, err := b.auditRecords(ctx, tenantID)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range records {
		if r.Time < start || r.Time >= end {
			continue
		}
		switch r.Category {
		case "login":
			login++
		case "operation":
			operation++
		}
	}
	return login, operation, nil
}

// GetLoginTrend 返回近 days 天每日登录次数（源 GetLoginTrend）。
//
// **核心语义（源注释原话）**：「按日期升序、**缺日补零**」——
// 没有任何登录的日子也必须出现在结果里（count=0），否则前端趋势图会断线。
// 这是本域最容易漏的细节，e2e 专门锁住。
func (b *Biz) GetLoginTrend(ctx context.Context, tenantID string, days int) ([]TrendPoint, error) {
	if days <= 0 {
		days = 7 // 源默认 7 天
	}

	records, err := b.auditRecords(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// 1) 预填 days 个日期桶（**升序**，含今天）。
	now := time.Now()
	loc := now.Location()
	buckets := make([]TrendPoint, days)
	idx := make(map[string]int, days)
	for i := 0; i < days; i++ {
		// 从 days-1 天前到 today，升序。
		d := dayStart(now.AddDate(0, 0, -(days - 1 - i)))
		key := d.Format("2006-01-02")
		buckets[i] = TrendPoint{Date: key, Count: 0}
		idx[key] = i
	}

	// 2) 把登录记录计入对应日期桶。
	earliest := dayStart(now.AddDate(0, 0, -(days - 1))).UnixNano()
	for _, r := range records {
		if r.Category != "login" || r.Time < earliest {
			continue
		}
		key := time.Unix(0, r.Time).In(loc).Format("2006-01-02")
		if i, ok := idx[key]; ok {
			buckets[i].Count++
		}
	}
	return buckets, nil
}

// GetOperationActionDistribution 返回操作审计按 action 的分布（源同名 rpc）。
func (b *Biz) GetOperationActionDistribution(ctx context.Context, tenantID string) ([]DistributionItem, error) {
	records, err := b.auditRecords(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	order := []string{} // 保持首次出现顺序（确定性输出）
	for _, r := range records {
		if r.Category != "operation" || r.Action == "" {
			continue
		}
		if _, seen := counts[r.Action]; !seen {
			order = append(order, r.Action)
		}
		counts[r.Action]++
	}
	items := make([]DistributionItem, 0, len(order))
	for _, k := range order {
		items = append(items, DistributionItem{Label: k, Count: counts[k]})
	}
	return items, nil
}

// GetLoginStatusDistribution 返回登录审计按 result 的分布（源同名 rpc）。
func (b *Biz) GetLoginStatusDistribution(ctx context.Context, tenantID string) ([]DistributionItem, error) {
	records, err := b.auditRecords(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	order := []string{}
	for _, r := range records {
		if r.Category != "login" || r.Result == "" {
			continue
		}
		if _, seen := counts[r.Result]; !seen {
			order = append(order, r.Result)
		}
		counts[r.Result]++
	}
	items := make([]DistributionItem, 0, len(order))
	for _, k := range order {
		items = append(items, DistributionItem{Label: k, Count: counts[k]})
	}
	return items, nil
}

// auditRecords 取审计记录（显式租户过滤，见包注释的多租户语义说明）。
func (b *Biz) auditRecords(ctx context.Context, tenantID string) ([]*authmodel.AuditRecord, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	records, _, err := bootstrappkg.AuditStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list audit records: %w", err)
	}
	return records, nil
}

// 保留 storev1 引用（Where 的 Filters 类型来自该包）。
var _ = storev1.FilterCondition{}
