// Package auditlogd 是 auditlog 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 本域 biz 只读（审计查询，数据由写路径审计落库），构造仅单仓储——全 8 域中
// 形态最简。
package auditlogd

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"

	gingonic "github.com/gin-gonic/gin"
	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// Store 审计记录仓储（InitBridges 的 repos.Audit 同一实例，无双构造）。
	Store func() *store.Store[authmodel.AuditRecord]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 auditlog 域的装配声明值（Routes 委托 hgin.RegisterAudit、
// Services 委托 secretgrpc.NewAuditServer——传输适配不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *auditlogbiz.Biz
	return appkit.Domain{
		Name: "auditlog",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st := d.Store()
			if st == nil {
				return fmt.Errorf("auditlogd: audit store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = auditlogbiz.New(st)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("auditlogd: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterAudit(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			auditv1.RegisterAuditServiceServer(s, secretgrpc.NewAuditServer(biz))
		},
	}
}
