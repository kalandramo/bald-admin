// Package mfad 是 mfa 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无 proto 纯 gin 面。挑战存储（Redis）由装配层 BeforeStart 随 TokenStore 同点抉择（无 Redis 保持 nil——MFA 安全边界 fail-closed），域经 Deps getter 迟到取用。
package mfad

import (
	"context"
	"net/http"

	mfabiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/mfa"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps——域 Wire 晚于
// 业务 BeforeStart 执行，届时仓储/认证器均已就绪）。
type Deps struct {
	// FactorStore UserMFAFactor 仓储（InitBridges 的 repos.MFAFactor 同一实例，无双构造）。
	FactorStore func() *store.Store[authmodel.UserMFAFactor]
	// Challenges MFA 挑战存储（装配层 BeforeStart 抉择的 Redis 存储；nil=无
	// Redis 禁用态，biz 判 nil 返回错误 fail-closed——MFA 是安全边界）。
	Challenges func() mfa.ChallengeStore
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 mfa 域的装配声明值。Routes 委托 hgin.RegisterMFA——传输适配
// 不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *mfabiz.Biz
	return appkit.Domain{
		Name: "mfa",
		Wire: func(_ context.Context, _ *appkit.App) error {
			st, err := domains.RequireStore("mfad", "store", d.FactorStore())
			if err != nil {
				return err
			}
			biz = mfabiz.New(st, d.Challenges())
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("mfad", h)
			if err != nil {
				return err
			}
			hgin.RegisterMFA(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	}
}
