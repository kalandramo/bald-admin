// Package messaged 是 message 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无 proto 纯 gin 面。SSE 实时推送在 SSE server 构建点经装配回调 SetPublisher 注入——域经 Biz() 访问器被装配层迟到读取（见下方 wired 注释）。
package messaged

import (
	"context"
	"net/http"

	msgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/message"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps——域 Wire 晚于
// 业务 BeforeStart 执行，届时仓储/认证器均已就绪）。
type Deps struct {
	// MessageStore InternalMessage 仓储（InitBridges 的 repos.Message 同一实例，无双构造）。
	MessageStore func() *store.Store[authmodel.InternalMessage]
	// CategoryStore InternalMessageCategory 仓储（InitBridges 的 repos.MessageCategory 同一实例，无双构造）。
	CategoryStore func() *store.Store[authmodel.InternalMessageCategory]
	// RecipientStore InternalMessageRecipient 仓储（InitBridges 的 repos.Recipient 同一实例，无双构造）。
	RecipientStore func() *store.Store[authmodel.InternalMessageRecipient]
	// UserStore User 仓储（InitBridges 的 repos.User 同一实例，无双构造）。
	UserStore func() *store.Store[authmodel.User]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// wired 是域 Wire 构造的 biz 端口。装配层 SSE 回调（SSE server 构建时点，
// 晚于域 Wire）经 Biz() 迟到读取，把 SSE server 适配成 Publisher 注入（同一
// biz 实例，无双构造）。
var wired *msgbiz.Biz

// Biz 返回 message 域的 biz 端口（未 Wire 时 nil——SSE 回调判空即跳过接线）。
func Biz() *msgbiz.Biz { return wired }

// Domain 返回 message 域的装配声明值。Routes 委托 hgin.RegisterMessage——传输适配
// 不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *msgbiz.Biz
	return appkit.Domain{
		Name: "message",
		Wire: func(_ context.Context, _ *appkit.App) error {
			ms, err := domains.RequireStore("messaged", "store", d.MessageStore())
			if err != nil {
				return err
			}
			cs, err := domains.RequireStore("messaged", "store", d.CategoryStore())
			if err != nil {
				return err
			}
			rs, err := domains.RequireStore("messaged", "store", d.RecipientStore())
			if err != nil {
				return err
			}
			us, err := domains.RequireStore("messaged", "store", d.UserStore())
			if err != nil {
				return err
			}
			biz = msgbiz.New(ms, cs, rs, us)
			wired = biz
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("messaged", h)
			if err != nil {
				return err
			}
			hgin.RegisterMessage(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
