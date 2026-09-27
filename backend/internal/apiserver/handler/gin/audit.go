package gin

// audit.go 审计日志查询 REST handler（T6）。分组级 Authn + 路由级 Authz
// （P9 归一化权限点 "audit"——REST /v1/audit 与 gRPC AuditService 同源，
// casbin 策略单写覆盖双协议）。只读接口（list/get），admin 专属。
// 复用 pb.go 的 bindPB/writePB 与本包 toAuditPB 转换。

import (
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	auditbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterAudit 挂载审计查询路由（需认证 + audit 权限，admin 专属）。
//   - GET /v1/audit      分页查询（?category=&subject=&object=&action=&result=&ip=&page_size=&page_token=）
//   - GET /v1/audit/:id  单条详情
func RegisterAudit(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *auditbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/audit", authzMW, func(c *gingonic.Context) {
		// GET 无 body，分页参数从 query 读（pagingFromQuery 定义在同包的
		// org.go——参数名以 grpc-gateway 约定为准：paging.page_size 等）。
		res, err := biz.List(c.Request.Context(), auditbiz.ListFilter{
			Category: c.Query("category"),
			Subject:  c.Query("subject"),
			Object:   c.Query("object"),
			Action:   c.Query("action"),
			Result:   c.Query("result"),
			IP:       c.Query("ip"),
		}, pagingFromQuery(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items := make([]*auditv1.AuditRecord, 0, len(res.Items))
		for _, r := range res.Items {
			items = append(items, convert.AuditRecordToPB(r))
		}
		// 分页元数据由 biz 侧 ListWithPaging 填充（total/next_token 等）。
		writePB(c, http.StatusOK, &auditv1.ListAuditRecordsResponse{
			Items: items,
			Meta:  res.Meta,
		})
	})

	authed.GET("/audit/:id", authzMW, func(c *gingonic.Context) {
		m, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &auditv1.GetAuditRecordResponse{Record: convert.AuditRecordToPB(m)})
	})
}
