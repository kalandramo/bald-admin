package gin

// user.go 用户管理 REST handler（T2）。租户内读写：跨租户访问由 P8 自动拦为 404；
// 授权经 P9 归一化权限点（user + get/post/put/delete），admin 全权、viewer 只读。

import (
	"context"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	web "github.com/kalandramo/bald/transport/web"

	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterUser 挂载用户管理路由。
//   - GET    /v1/user       列出当前租户用户（原 SecretService.ListUsers 接管点）
//   - GET    /v1/user/:id   取用户（租户内）
//   - POST   /v1/user       创建（自动归属 ctx 租户）
//   - PUT    /v1/user/:id   更新
//   - DELETE /v1/user/:id   删除
func RegisterUser(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *userbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/user", authzMW, func(c *gingonic.Context) {
		users, err := biz.List(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items := make([]*userv1.User, 0, len(users))
		for _, u := range users {
			items = append(items, convert.UserToPB(u))
		}
		writePB(c, http.StatusOK, &userv1.ListUsersResponse{Users: items, Total: uint32(len(items))})
	})

	authed.GET("/user/:id", authzMW, func(c *gingonic.Context) {
		u, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &userv1.GetUserResponse{User: convert.UserToPB(u)})
	})

	authed.POST("/user", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
				u, err := biz.Create(ctx, req.GetId(), req.GetUsername(), req.GetRoles(), req.GetPassword())
				if err != nil {
					return nil, err // 校验→400、ID 冲突→409、内部→500
				}
				return &userv1.CreateUserResponse{User: convert.UserToPB(u)}, nil
			})
	})

	authed.PUT("/user/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
				u, err := biz.Update(ctx, c.Param("id"), req.GetUsername(), req.GetRoles(), req.GetPassword())
				if err != nil {
					return nil, err // 此前 NotFound/内部错误一律折叠 400
				}
				return &userv1.UpdateUserResponse{User: convert.UserToPB(u)}, nil
			})
	})

	authed.DELETE("/user/:id", authzMW, func(c *gingonic.Context) {
		ok, err := biz.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		if !ok {
			web.ErrorResponse(c, berrors.NotFound("user/not_found").WithMessage("user not found"))
			return
		}
		writePB(c, http.StatusOK, &userv1.DeleteUserResponse{Deleted: c.Param("id")})
	})
}
