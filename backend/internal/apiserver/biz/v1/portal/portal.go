// Package portal 是管理面聚合业务（Wave 6.2，自 go-wind-admin admin 域
// i_admin_portal.proto 精简移植，3 rpc）。
//
// ## 跨域聚合（本包存在的理由）
//
// 三个 rpc 都是**聚合视图**：把 user/role/permission/menu 四域数据拼成前端
// 初始化所需的结构，无单一对标 biz。聚合链：
//
//	User.Roles(角色名) → Role.Perms(权限码) → Permission.MenuIDs(菜单ID) → Menu 树
//
// ## 关键语义（对齐源 admin_portal_service.go）
//
//   - GetNavigation：当前用户可见的菜单树——**去 BUTTON 类型**、**只留 ON 状态**、
//     按 Order 升序、ParentID 组树（源的 fillRouteItem 同语义）；
//   - GetMyPermissionCode：当前用户的权限码列表（源同）；
//   - GetInitialContext：两者合并（源同）。
//
// ## 未移植项（称量）
//
// 源的 `filterMenusByPlanWhitelist`（按租户套餐白名单过滤菜单）**未移植**：
// 本项目 plan_module 的 module 字段语义未对齐（源用枚举，本项目未落该字段），
// 强行实现会造「无数据来源」的逻辑——违背「不造无消费者」原则。已在 proto
// 头注记录。
package portal

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/contextx"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrNoUser 无法从上下文取到当前用户（未认证）。
var ErrNoUser = errors.New("portal: no authenticated user in context")

// Biz 管理面聚合业务。
type Biz struct{}

// New 构造 portal 业务。
func New() *Biz { return &Biz{} }

func (b *Biz) userStore() *store.Store[authmodel.User]       { return bootstrappkg.UserStore }
func (b *Biz) roleStore() *store.Store[authmodel.Role]       { return bootstrappkg.RoleStore }
func (b *Biz) permStore() *store.Store[authmodel.Permission] { return bootstrappkg.PermissionStore }
func (b *Biz) menuStore() *store.Store[authmodel.Menu]       { return bootstrappkg.MenuStore }

// RouteItem 是前端路由项（扁平结构，见 proto 头注）。
type RouteItem struct {
	Path      string
	Name      string
	Component string
	Redirect  string
	Title     string
	Icon      string
	Order     int32
	Children  []*RouteItem
}

// permissionCodes 取当前用户的权限码列表（聚合链前半段）。
//
// User.Roles(CSV 角色名) → Role.Perms(CSV 权限码)，去重后返回。
func (b *Biz) permissionCodes(ctx context.Context, userID string) ([]string, error) {
	u, err := b.getUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	codes := map[string]bool{}
	for _, roleName := range u.RolesList() {
		r, err := b.getRole(ctx, roleName)
		if err != nil {
			// 角色不存在不报错（用户可能引用了已删角色）——跳过，与源宽容语义一致。
			continue
		}
		for _, c := range r.PermsList() {
			codes[c] = true
		}
	}
	out := make([]string, 0, len(codes))
	for c := range codes {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// menuIDs 取权限码关联的菜单 ID 集合（聚合链后半段）。
//
// 权限码 → Permission.MenuIDs(CSV)，去重后返回。
func (b *Biz) menuIDs(ctx context.Context, codes []string) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, code := range codes {
		p, err := b.getPermission(ctx, code)
		if err != nil {
			continue // 权限点未注册（Role.Perms 可引用未注册码）——跳过
		}
		for _, id := range p.MenuIDsList() {
			ids[id] = true
		}
	}
	return ids, nil
}

// GetNavigation 当前用户的导航路由表（菜单树，去 BUTTON、只留 ON）。
func (b *Biz) GetNavigation(ctx context.Context, userID string) ([]*RouteItem, error) {
	codes, err := b.permissionCodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids, err := b.menuIDs(ctx, codes)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil // 无关联菜单 → 空导航（非错误）
	}

	// 取全部菜单（平台级数据，无租户维度），过滤出授权 ID 集合内的项。
	all, _, err := b.menuStore().List(ctx, &store.Where{})
	if err != nil {
		return nil, fmt.Errorf("portal: list menus: %w", err)
	}
	visible := make([]*authmodel.Menu, 0, len(all))
	for _, m := range all {
		if !ids[m.ID] {
			continue
		}
		if m.Type == "BUTTON" {
			continue // 路由不含按钮
		}
		if m.Status != "" && m.Status != "ON" {
			continue // 只留启用
		}
		visible = append(visible, m)
	}
	return buildRoutes(visible), nil
}

// GetMyPermissionCode 当前用户的权限码列表。
func (b *Biz) GetMyPermissionCode(ctx context.Context, userID string) ([]string, error) {
	return b.permissionCodes(ctx, userID)
}

// GetInitialContext 菜单树 + 权限码（一次拉全）。
func (b *Biz) GetInitialContext(ctx context.Context, userID string) ([]*RouteItem, []string, error) {
	routes, err := b.GetNavigation(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	codes, err := b.GetMyPermissionCode(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return routes, codes, nil
}

// CurrentUserID 从 ctx 取当前登录用户 ID（未认证 → 错误）。
//
// 与 handler 的认证中间件同源：userID 经 contextx 注入（authn 中间件写入）。
func CurrentUserID(ctx context.Context) (string, error) {
	if id := contextx.UserIDFromContext(ctx); id != "" {
		return id, nil
	}
	return "", berrors.Unauthenticated("portal/unauthenticated").
		WithMessage("portal: no authenticated user")
}

// getUser 按 ID 取用户。
func (b *Biz) getUser(ctx context.Context, id string) (*authmodel.User, error) {
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", id))
	u, err := b.userStore().Get(ctx, w)
	if err != nil || u == nil {
		return nil, berrors.NotFound("portal/user_not_found")
	}
	return u, nil
}

// getRole 按角色名取角色。
func (b *Biz) getRole(ctx context.Context, name string) (*authmodel.Role, error) {
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", name))
	return b.roleStore().Get(ctx, w)
}

// getPermission 按权限码取权限点。
func (b *Biz) getPermission(ctx context.Context, code string) (*authmodel.Permission, error) {
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", code))
	return b.permStore().Get(ctx, w)
}

// buildRoutes 把菜单列表组树为路由项（ParentID 嵌套，Order 升序）。
//
// 与 menu biz 的 buildTree 同语义，但输出 RouteItem（去 BUTTON 已在调用方完成）。
// 用中间映射 itemByMenuID 按**菜单 ID**挂子节点（RouteItem 无 ID 字段）。
func buildRoutes(menus []*authmodel.Menu) []*RouteItem {
	byID := map[string]*authmodel.Menu{}
	for _, m := range menus {
		byID[m.ID] = m
	}
	itemByMenuID := make(map[string]*RouteItem, len(menus))
	for _, m := range menus {
		itemByMenuID[m.ID] = toRouteItem(m)
	}

	var roots []*RouteItem
	for _, m := range menus {
		it := itemByMenuID[m.ID]
		parent := itemByMenuID[m.ParentID]
		if m.ParentID == "" || byID[m.ParentID] == nil || parent == nil {
			roots = append(roots, it) // 根节点（或无父/父不可见 → 提升为根）
			continue
		}
		parent.Children = append(parent.Children, it)
	}
	sortRoutes(roots)
	return roots
}

// sortRoutes 按 Order 递归排序。
func sortRoutes(items []*RouteItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Order < items[j].Order })
	for _, it := range items {
		sortRoutes(it.Children)
	}
}

// toRouteItem 菜单 → 路由项。
func toRouteItem(m *authmodel.Menu) *RouteItem {
	return &RouteItem{
		Path:      m.Path,
		Name:      m.Name,
		Component: m.Component,
		Title:     m.Title,
		Icon:      m.Icon,
		Order:     m.Order,
	}
}
