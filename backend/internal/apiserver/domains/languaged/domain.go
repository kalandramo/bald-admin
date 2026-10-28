// Package languaged 是 language 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 独立成包的判定依据：biz/v1/language 与 dict 包零 import 关系、
// gin/gRPC 面亦无共享 handler——单独 Name=language。
// 本域无 gRPC 面（handler/grpc 无对应 server），
// 故不设 Services。
package languaged

import (
	"context"
	"net/http"

	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// Store 语言仓储（InitBridges 的 repos.Language 同一实例，无双构造）。
	Store func() *store.Store[authmodel.Language]
	// Authn 认证器（带吊销检查；路由节点执行时已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；路由节点执行时已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 language 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain）：biz 节点构造领域能力，routes 节点委托
// hgin.RegisterLanguage——传输适配不搬家；无 gRPC 面故无 Services。
func Domain(d Deps) appkit.Domain {
	var biz *langbiz.Biz
	return domains.StandardDomain("language", "languaged",
		func(context.Context, *appkit.App) error {
			st, err := domains.RequireStore("languaged", "language store", d.Store())
			if err != nil {
				return err
			}
			biz = langbiz.New(st)
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("languaged", h)
			if err != nil {
				return err
			}
			hgin.RegisterLanguage(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
}
