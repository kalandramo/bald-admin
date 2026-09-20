package gin

// admin_portal.go 管理面聚合 REST handler（Wave 6.2）。路径前缀 /admin/v1/*，
// 对齐源的 admin 域约定。分组级 Authn（/admin/v1 需登录）+ 路由级 Authz
// （P9 归一化权限点 "admin_portal"）。

import (
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/admin/v1"
	portalbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/portal"
)

// RegisterAdminPortal 挂载管理面聚合路由（需认证 + admin_portal 权限）。
//   - GET /admin/v1/routes           前端路由表
//   - GET /admin/v1/perm-codes       当前用户权限码
//   - GET /admin/v1/initial-context  菜单树 + 权限码
func RegisterAdminPortal(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *portalbiz.Biz,
) {
	authed := e.Group("/admin/v1")
	authed.Use(authnMiddleware(authenticator))
	// **自定义 ObjectResolver**：本组路由的 object 固定为 "admin_portal"。
	//
	// 为何不用 DefaultHTTPObject：它取路径首段 → /admin/v1/routes 得 "admin"，
	// 与 /admin/components（管理面组件目录，admin 专属）**撞名**——给 viewer
	// 开 "admin" 只读会连带放开组件目录（实测回归 TestAdmin_Forbidden 由 403
	// 变 200）。用独立 object 隔离两者。
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(func(string) string { return "admin_portal" }),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/routes", authzMW, func(c *gingonic.Context) {
		uid, err := portalbiz.CurrentUserID(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items, err := biz.GetNavigation(c.Request.Context(), uid)
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &adminv1.ListRouteResponse{Items: toRouteItemsPB(items)})
	})

	authed.GET("/perm-codes", authzMW, func(c *gingonic.Context) {
		uid, err := portalbiz.CurrentUserID(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		codes, err := biz.GetMyPermissionCode(c.Request.Context(), uid)
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &adminv1.ListPermissionCodeResponse{Codes: codes})
	})

	authed.GET("/initial-context", authzMW, func(c *gingonic.Context) {
		uid, err := portalbiz.CurrentUserID(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		routes, codes, err := biz.GetInitialContext(c.Request.Context(), uid)
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &adminv1.InitialContextResponse{
			Menus:       toRouteItemsPB(routes),
			Permissions: codes,
		})
	})
}

// toRouteItemsPB 递归转换路由项。
func toRouteItemsPB(items []*portalbiz.RouteItem) []*adminv1.MenuRouteItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]*adminv1.MenuRouteItem, 0, len(items))
	for _, it := range items {
		out = append(out, &adminv1.MenuRouteItem{
			Path:      it.Path,
			Name:      it.Name,
			Component: it.Component,
			Redirect:  it.Redirect,
			Title:     it.Title,
			Icon:      it.Icon,
			Order:     it.Order,
			Children:  toRouteItemsPB(it.Children),
		})
	}
	return out
}
