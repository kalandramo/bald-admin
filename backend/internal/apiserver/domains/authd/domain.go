// Package authd 是 auth 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 重域特殊点：登录防护三件
// （限流/熔断/重试）的构造器住本包（config.go），读业务自持
// 配置段 biz.login.*：域 Wire 自读 Settings 快照的 login 段
// （decodeLogin：段内严格解码 + 校验）并自行构造，装配层不再出现
// 业务阈值；命令行 flag 通道经 FlagOptions（PlainBinder）保留——装配层仅
// Bind 声明，键名与默认值仍归本包。
//
// biz.login 是**可协调节点**（ReloadReconcile）：配置热更时本域 Reconcile
// 重新解码同一快照并整体替换登录防护三件（authbiz.ReplaceLoginGuards，
// nil = 真的禁用），无需重启。故本域不用 domains.StandardDomain 的通用
// 拓扑，而是手写 Declare/ApplyNode/Reconcile。
//
// 其余独有依赖（TokenStore/验证码/平台判据/令牌 TTL）按 file 域先例：基础设施
// 构造留装配层（BeforeStart 抉择），域经 Deps getter 迟到取用。auth biz 的
// setter 全部「nil 不覆盖」，getter 返回零值即禁用态。
// 含「无 Redis 时认证器退化为纯验签」：token.NewRevocationChecker
// 对 nil store 返回原认证器，装配层单一 Authn getter 即覆盖原两分支。
package authd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	authnjwt "github.com/kalandramo/bald/contrib/authn-jwt"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"

	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/token"
)

// Deps 域的外部依赖（装配层 getter 注入；getter 语义同 tenantd.Deps——域 Wire
// 晚于业务 BeforeStart 执行，届时仓储/认证器/TokenStore 均已就绪）。
type Deps struct {
	// Signer 令牌签发器（bootstrap.Signer，bald-authn-jwt RSA 实例）。
	Signer func() authnjwt.Signer
	// UserStore / TenantStore 认证链路双仓储（InitBridges 产物，同一实例）。
	UserStore   func() *store.Store[authmodel.User]
	TenantStore func() *store.Store[authmodel.Tenant]
	// ReloadPolicies casbin 策略热重载回调（bootstrap.ReloadPolicies 同一函数）。
	ReloadPolicies func() func() error
	// Authn 运行期认证器（带吊销检查；无 Redis 时即原始验签器——同一变量）。
	Authn func() authn.Authenticator
	// TokenStore 令牌服务端状态存储（buildTokenStore 产物；nil=无 Redis 禁用态）。
	TokenStore func() token.Store
	// CaptchaStore 验证码存储（复用同一 Redis；nil=不可用，fail-closed）。
	CaptchaStore func() captcha.Store
	// PlatformResolver 平台级身份判据（authmodel.IsPlatformUser，纯函数）。
	PlatformResolver func() func(*authmodel.User) bool
	// TokenTTL 令牌有效期（业务自持 auth.access_ttl_* 段的装配层抉择产物；
	// access<=0 返回零值，SetTokenTTL 对零值忽略=默认 TTL）。
	TokenTTL func() (access, refresh time.Duration)
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

const (
	bizKey    = appkit.ResourceKey("domain.auth.biz")
	routesKey = appkit.ResourceKey("domain.auth.routes")
)

// Domain 返回 auth 域的装配声明值（biz+routes 声明节点；biz 节点读取
// biz.login 段并以 ReloadReconcile 参与热更，routes 委托 hgin.RegisterAuth——
// 传输适配不搬家）；auth 无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *authbiz.Biz
	wire := func(ctx context.Context, app *appkit.App) error {
		// 登录防护三件的本域配置（biz.login.*）：域自读 Settings 快照，
		// 段内严格解码 + 校验，非法配置在此 fail-fast
		// （启动期错误，不静默）。
		cfg, err := decodeLogin(app.Settings())
		if err != nil {
			return err
		}
		us, err := domains.RequireStore("authd", "user/tenant store", d.UserStore())
		if err != nil {
			return err
		}
		ts, err := domains.RequireStore("authd", "user/tenant store", d.TenantStore())
		if err != nil {
			return err
		}
		biz = authbiz.New(d.Signer(), us, ts, d.ReloadPolicies())
		// 登录防护三件（nil=未配置禁用态，
		// biz 侧 SetLoginXxx 对 nil 不覆盖）。
		biz.SetLoginLimiter(buildLoginLimiter(cfg))
		biz.SetLoginBreaker(buildLoginBreaker(cfg))
		biz.SetLoginRetrier(buildLoginRetrier(cfg))
		// 平台判据（纯函数，无条件注入）/令牌/验证码/TTL：装配层已抉择
		// 产物，getter 返回零值即禁用态（setter nil 不覆盖）。
		// 依赖缺席即保持禁用态，不报错。
		biz.SetPlatformResolver(d.PlatformResolver())
		biz.SetTokenStore(d.TokenStore())
		biz.SetAuthenticator(d.Authn())
		biz.SetCaptchaStore(d.CaptchaStore())
		access, refresh := d.TokenTTL()
		biz.SetTokenTTL(access, refresh)
		return nil
	}
	mount := func(h http.Handler) error {
		e, err := domains.AssertGin("authd", h)
		if err != nil {
			return err
		}
		hgin.RegisterAuth(e, d.Authn(), d.Authz(), biz)
		return nil
	}

	return appkit.Domain{
		Name: "auth",
		Declare: func(_ context.Context, s appkit.PlanScope) ([]appkit.Node, error) {
			nodes := []appkit.Node{{
				Key:      bizKey,
				Segments: []string{"biz." + ConfigSection},
				Phase:    appkit.PhaseWire,
				Priority: 10,
				Reload:   appkit.ReloadReconcile,
			}}
			if s.Decl.HTTPHandler == nil {
				return nodes, nil
			}
			return append(nodes, appkit.Node{
				Key:       routesKey,
				Segments:  []string{"server.http"},
				Phase:     appkit.PhaseServe,
				Priority:  10,
				DependsOn: []appkit.ResourceKey{bizKey},
			}), nil
		},
		ApplyNode: func(ctx context.Context, app *appkit.App, node appkit.Node) error {
			switch node.Key {
			case bizKey:
				return wire(ctx, app)
			case routesKey:
				h := app.HTTPHandler()
				if h == nil {
					return fmt.Errorf("authd: declared node %q has no HTTP face at execution time", node.Key)
				}
				return mount(h)
			}
			return fmt.Errorf("authd: unknown declared node %q", node.Key)
		},
		Reconcile: func(_ context.Context, app *appkit.App, node appkit.Node) error {
			if node.Key != bizKey {
				return nil // routes 节点是静态挂载，无需协调
			}
			if biz == nil {
				return fmt.Errorf("authd: reconcile %q before wire", node.Key)
			}
			// 与 Wire 同一解码/校验路径：热更配置非法时本次协调失败（框架记
			// Drift/日志），旧三件保持生效，不做半切换。
			cfg, err := decodeLogin(app.Settings())
			if err != nil {
				return err
			}
			biz.ReplaceLoginGuards(buildLoginLimiter(cfg), buildLoginBreaker(cfg), buildLoginRetrier(cfg))
			return nil
		},
	}
}
