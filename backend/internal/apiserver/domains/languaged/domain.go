// Package languaged 是 language 域的装配声明包（批 4 第二波，机械域）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注（共用同一裁决）。
//
// 独立成包的判定依据：biz/v1/language 与 dict 包零 import 关系（任务手册的
// 「依附则随 dict」不成立），gin/gRPC 面亦无共享 handler——单独 Name=language。
// 本域无 gRPC 面（handler/grpc 无对应 server，迁移前 registerGRPC 亦无对应行），
// 故不设 Services。
package languaged

import (
	"context"
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"
	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
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
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 language 域的装配声明值（Routes 委托 hgin.RegisterLanguage——
// 传输适配不搬家，机械迁移；无 gRPC 面故无 Services）。
func Domain(d Deps) appkit.Domain {
	var biz *langbiz.Biz
	return appkit.Domain{
		Name: "language",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st := d.Store()
			if st == nil {
				return fmt.Errorf("languaged: language store not ready " +
					"(InitBridges must run in business BeforeStart before domain Wire)")
			}
			biz = langbiz.New(st)
			return nil
		},
		Routes: func(h http.Handler) error {
			e, ok := h.(*gingonic.Engine)
			if !ok {
				return fmt.Errorf("languaged: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", h)
			}
			hgin.RegisterLanguage(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
