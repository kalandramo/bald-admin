package gin

// tenant.go 租户管理 REST handler（T2）。路由挂载遵循仓库双范式：
// 分组级 Authn（/v1 需登录）+ 路由级 Authz（P9 归一化权限点）。
// REST 路径用单数段（/v1/tenant），与 gRPC DefaultGRPCObject("TenantService/...")="tenant"
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

	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterTenant 挂载租户管理路由（全部需认证 + tenant 资源权限，casbin 仅授予 admin）。
//   - GET    /v1/tenant       列出租户（平台全量）
//   - GET    /v1/tenant/:id   取租户
//   - POST   /v1/tenant       创建
//   - PUT    /v1/tenant/:id   更新
//   - DELETE /v1/tenant/:id   删除（platform 受保护）
func RegisterTenant(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *tenantbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/tenant", authzMW, func(c *gingonic.Context) {
		ts, err := biz.List(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items := make([]*tenantv1.Tenant, 0, len(ts))
		for _, t := range ts {
			items = append(items, convert.TenantToPB(t))
		}
		writePB(c, http.StatusOK, &tenantv1.ListTenantsResponse{Items: items, Total: uint32(len(items))})
	})

	authed.GET("/tenant/:id", authzMW, func(c *gingonic.Context) {
		t, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500（不再一刀切折叠）
			return
		}
		writePB(c, http.StatusOK, &tenantv1.GetTenantResponse{Tenant: convert.TenantToPB(t)})
	})

	authed.POST("/tenant", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *tenantv1.CreateTenantRequest) (*tenantv1.CreateTenantResponse, error) {
				t, err := biz.Create(ctx, req.GetId(), req.GetName(), req.GetRemark())
				if err != nil {
					return nil, err // 校验→400、编码冲突→409、内部→500
				}
				return &tenantv1.CreateTenantResponse{Tenant: convert.TenantToPB(t)}, nil
			})
	})

	authed.PUT("/tenant/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *tenantv1.UpdateTenantRequest) (*tenantv1.UpdateTenantResponse, error) {
				// STATUS_UNSPECIFIED 表示「不改」，映射为空串交给 biz 跳过校验。
				status := ""
				if req.GetStatus() != tenantv1.Tenant_STATUS_UNSPECIFIED {
					status = req.GetStatus().String()
				}
				t, err := biz.Update(ctx, c.Param("id"), req.GetName(), status, req.GetRemark())
				if err != nil {
					return nil, err // 此前 NotFound/冲突/内部错误一律折叠 400
				}
				return &tenantv1.UpdateTenantResponse{Tenant: convert.TenantToPB(t)}, nil
			})
	})

	authed.DELETE("/tenant/:id", authzMW, func(c *gingonic.Context) {
		ok, err := biz.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // platform 保护→400、NotFound→404、内部→500
			return
		}
		if !ok {
			web.ErrorResponse(c, berrors.NotFound("tenant/not_found").WithMessage("tenant not found"))
			return
		}
		writePB(c, http.StatusOK, &tenantv1.DeleteTenantResponse{Deleted: c.Param("id")})
	})
}
