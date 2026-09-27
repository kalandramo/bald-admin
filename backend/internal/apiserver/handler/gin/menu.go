package gin

// menu.go 菜单管理 REST handler（T3）。路由挂载遵循仓库双范式：
// 分组级 Authn（/v1 需登录）+ 路由级 Authz（P9 归一化权限点）。
// REST 路径用单数段（/v1/menu），与 gRPC DefaultGRPCObject("MenuService/...")="menu"
// 同源——casbin 策略单写即覆盖双协议。

import (
	"context"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	web "github.com/kalandramo/bald/transport/web"

	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterMenu 挂载菜单管理路由（全部需认证 + menu 资源权限，admin 写 / viewer 读）。
//   - GET    /v1/menu       菜单树
//   - GET    /v1/menu/:id   取节点
//   - POST   /v1/menu       创建
//   - PUT    /v1/menu/:id   更新
//   - DELETE /v1/menu/:id   删除（级联子树）
func RegisterMenu(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *menubiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/menu", authzMW, func(c *gingonic.Context) {
		roots, total, err := biz.ListTree(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items := make([]*menuv1.Menu, 0, len(roots))
		for _, m := range roots {
			items = append(items, convert.MenuToPBTree(m))
		}
		writePB(c, http.StatusOK, &menuv1.ListMenusResponse{Items: items, Total: uint32(total)})
	})

	authed.GET("/menu/:id", authzMW, func(c *gingonic.Context) {
		m, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &menuv1.GetMenuResponse{Menu: convert.MenuToPB(m)})
	})

	authed.POST("/menu", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *menuv1.CreateMenuRequest) (*menuv1.CreateMenuResponse, error) {
				m, err := biz.Create(ctx, req.GetId(), req.GetParentId(),
					menuTypeString(req.GetType()), req.GetName(), req.GetPath(),
					req.GetComponent(), req.GetTitle(), req.GetIcon(), req.GetOrder(), req.GetRemark())
				if err != nil {
					return nil, err // 校验→400（biz 产 berrors）、NotFound→404、内部→500
				}
				return &menuv1.CreateMenuResponse{Menu: convert.MenuToPB(m)}, nil
			})
	})

	authed.PUT("/menu/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *menuv1.UpdateMenuRequest) (*menuv1.UpdateMenuResponse, error) {
				// UNSPECIFIED 表示「不改」，映射空串交给 biz 跳过；order 用显式 order_set 位。
				m, err := biz.Update(ctx, c.Param("id"),
					req.GetParentId(), menuTypeString(req.GetType()), req.GetName(), req.GetPath(),
					req.GetComponent(), req.GetTitle(), req.GetIcon(), req.GetOrder(), req.GetOrderSet(),
					menuStatusString(req.GetStatus()), req.GetRemark())
				if err != nil {
					return nil, err // 校验→400（biz 产 berrors）、NotFound→404、内部→500
				}
				return &menuv1.UpdateMenuResponse{Menu: convert.MenuToPB(m)}, nil
			})
	})

	authed.DELETE("/menu/:id", authzMW, func(c *gingonic.Context) {
		deleted, err := biz.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // 校验→400（biz 产 berrors）、NotFound→404、内部→500
			return
		}
		if deleted == 0 {
			web.ErrorResponse(c, berrors.NotFound("menu/not_found").WithMessage("menu not found"))
			return
		}
		writePB(c, http.StatusOK, &menuv1.DeleteMenuResponse{Deleted: c.Param("id")})
	})
}

// menuTypeString / menuStatusString proto 枚举 → 存储字符串（UNSPECIFIED=不改→空串）。
func menuTypeString(t menuv1.Menu_Type) string {
	if t == menuv1.Menu_TYPE_UNSPECIFIED {
		return ""
	}
	return t.String()[len("TYPE_"):] // "TYPE_MENU" → "MENU"
}

func menuStatusString(s menuv1.Menu_Status) string {
	if s == menuv1.Menu_STATUS_UNSPECIFIED {
		return ""
	}
	return s.String()[len("STATUS_"):] // "STATUS_ON" → "ON"
}
