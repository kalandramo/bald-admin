// Package auth 实现 M1 认证授权范本的业务逻辑（biz 层）。
//
// 本层不依赖 gin/grpc：业务函数以纯 Go 入参/出参呈现，由 handler 层负责协议
// 转换（HTTP/gRPC transcoding）。这是 osbuilder/bald 推荐的分层：
// handler（协议）→ biz（业务）→ store（数据）。
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	authnjwt "github.com/kalandramo/bald/contrib/authn-jwt"
	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/circuitbreaker"
	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/store"
	"github.com/kalandramo/bald/ratelimit"
	"github.com/kalandramo/bald/retry"
	"golang.org/x/crypto/bcrypt"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrBadCredential 凭据错误。
var ErrBadCredential = errors.New("auth: invalid username or password")

// ErrRateLimited 登录被限流（handler 层映射为 429）。
// Wave 1a：对齐源项目 LoginRateLimiter 的语义——「登录尝试过于频繁」，
// 与凭据错误（401）区分，便于前端提示「稍后重试」而非「密码错」。
var ErrRateLimited = errors.New("auth: too many login attempts")

// dummyHash 进程启动时预生成一次的 bcrypt 哈希：用户不存在路径做等代价比较，
// 抹平「用户存在与否」的响应时序差（防用户名枚举侧信道）——存在路径每次都跑
// bcrypt 比较，不存在路径若直接返回会快出数量级。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcrypt.DefaultCost)

// Credential 登录凭据。ClientIP/UserAgent 由 handler 层从协议请求提取传入
// （T6 登录审计落库用；biz 层保持协议无关）。
type Credential struct {
	Username  string
	Password  string
	ClientIP  string // 客户端 IP（gin: c.ClientIP()）
	UserAgent string // 客户端 UA（gin: c.Request.UserAgent()）
}

// TokenPair 登录成功后签发的令牌对。
// json tag 必备：HTTP handler 直接 c.JSON 序列化本结构（非 proto 消息不走
// writePB），无 tag 会按 Go 导出名输出（{"AccessToken":...}），前端取
// access_token 得 undefined，后续请求携带 "Bearer undefined" 触发 JWT 解析错。
type TokenPair struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"` // unix 秒
}

// UserInfo 当前登录用户信息（WhoAmI 返回）。
type UserInfo struct {
	Username  string   `json:"username"`
	UserID    string   `json:"user_id"`
	TenantID  string   `json:"tenant_id"`
	Roles     []string `json:"roles"`
	TokenType string   `json:"token_type"` // 如 "Bearer"
}

// ErrLoginUnavailable 登录依赖（DB）熔断打开——快速失败，不再等 DB 超时。
// handler 层映射为 503（服务暂不可用），提示客户端稍后重试。
var ErrLoginUnavailable = errors.New("auth: login temporarily unavailable")

// Biz 认证业务。
type Biz struct {
	signer authnjwt.Signer // 私钥签发器（非对称场景只持私钥）
	// loginLimiter 登录限流器（Wave 1a，bald ratelimit/tokenbucket）。
	// nil = 禁用态：不阻断登录（对齐源项目 LoginRateLimiter 的 fail-open——
	// 限流是防御性增强，不应让限流组件故障导致登录全部不可用）。
	loginLimiter ratelimit.Limiter
	// loginBreaker 登录 DB 查询熔断器（Wave 1b，bald circuitbreaker/sres）。
	// nil = 禁用态。熔断的意义：DB 故障时快速失败，避免所有登录请求都卡在
	// 超时上（雪崩）。**只对真故障计数**——ErrNotFound（用户不存在）是正常
	// 业务结果，计入会让人用不存在的用户名熔断整个登录。
	loginBreaker circuitbreaker.CircuitBreaker
	// loginRetrier 登录 DB 查询重试器（Wave 1c，bald retry）。
	// nil = 禁用态（单次查询）。与熔断**天然组合**：retry 处理偶发抖动
	//（重试可恢复），熔断处理持续故障（重试无望时快速失败）。
	// 分类器排除 ErrNotFound——重试确定性失败只是浪费 DB 往返。
	loginRetrier *retry.Retrier
}

// RetryOnTransientDBError 是登录 DB 查询的重试分类器（Wave 1c）：
// **只重试瞬时故障**，不重试确定性结果。
//
//   - ErrNotFound（用户不存在）：false——确定性业务结果，重试无意义
//     （重试 3 次只是浪费 DB 往返，且拖慢响应）；
//   - nil：false——无错误无需重试；
//   - 其余（连接失败/超时等）：true——瞬时故障，重试可能恢复。
func RetryOnTransientDBError(err error) bool {
	if err == nil || errors.Is(err, store.ErrNotFound) {
		return false
	}
	return true
}

// New 构造认证 Biz。signer 来自 bootstrap（bald-authn-jwt 签发实例）。
func New(signer authnjwt.Signer) *Biz {
	return &Biz{signer: signer}
}

// SetLoginLimiter 运行期注入登录限流器（main.go 在装配后调用；测试可注入
// 真实 tokenbucket 实例）。nil 不覆盖——保留禁用态语义。
//
// 为什么经 setter 而非构造参数：与 SetCache/SetStorage 同款的 T0 时序约定——
// 限流器构造需要配置（rate/burst），而配置在 BeforeStart 才装载完成。
func (b *Biz) SetLoginLimiter(l ratelimit.Limiter) {
	if l != nil {
		b.loginLimiter = l
	}
}

// SetLoginBreaker 运行期注入登录 DB 查询熔断器（Wave 1b）。nil 不覆盖。
// 与 SetLoginLimiter 同款的运行期注入（配置在 BeforeStart 才装载完成）。
func (b *Biz) SetLoginBreaker(cb circuitbreaker.CircuitBreaker) {
	if cb != nil {
		b.loginBreaker = cb
	}
}

// SetLoginRetrier 运行期注入登录 DB 查询重试器（Wave 1c）。nil 不覆盖。
// 应与 SetLoginBreaker 配合使用（retry 处理抖动、熔断处理持续故障）。
func (b *Biz) SetLoginRetrier(r *retry.Retrier) {
	if r != nil {
		b.loginRetrier = r
	}
}

// Login 校验凭据（查 store）并签发 JWT。
// T6 登录审计：成功/失败/内部错误均记一条 category=login 审计事件（源
// login_audit_log 语义精简：IP/UA/结果/失败原因；风险评分/MFA/设备指纹后续迭代）。
// 经全局 audit.GetAuditor()（bootstrap audit.backends 热切换同一入口），旁路不阻断。
func (b *Biz) Login(ctx context.Context, c Credential) (*TokenPair, error) {
	// Wave 1a：限流在凭据校验之前——先拦流量再做昂贵的 bcrypt 比较，
	// 否则限流形同虚设（攻击者仍能消耗 CPU）。限流器为 nil 时直接放行
	//（禁用态，对齐源项目 LoginRateLimiter 的 fail-open）。
	if b.loginLimiter != nil {
		allowed, lerr := b.loginLimiter.Allow()
		switch {
		case !allowed:
			// 超限：Allow 契约规定 ok=false 伴随 ratelimit.ErrLimited——
			// 这是「被限流」的正常信号，不是故障，不可当作 fail-open 处理
			//（ratelimit.go 的 Limiter 接口注释）。
			auditLogin(ctx, c, "", audit.ResultDeny, "rate limited")
			return nil, ErrRateLimited
		case lerr != nil:
			// 限流器自身故障（非 ErrLimited）：fail-open 不阻断登录，
			// 但记录以便排查。
			log.Warn(ctx, "login rate limiter error, failing open", "error", lerr.Error())
		}
	}

	u, err := b.queryUser(ctx, c.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password)) // 等代价比较，见 dummyHash 注释
			auditLogin(ctx, c, "", audit.ResultDeny, "invalid credentials")
			return nil, ErrBadCredential
		}
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			// 熔断打开：DB 已不可用，快速失败（不再等超时）。
			auditLogin(ctx, c, "", audit.ResultError, "login dependency circuit open")
			return nil, ErrLoginUnavailable
		}
		auditLogin(ctx, c, "", audit.ResultError, err.Error())
		return nil, fmt.Errorf("auth: query user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)); err != nil {
		auditLogin(ctx, c, u.TenantID, audit.ResultDeny, "invalid credentials")
		return nil, ErrBadCredential
	}

	now := time.Now()
	ttl := 2 * time.Hour
	claims := authn.AuthClaims{
		Issuer:   "go-bald-admin",
		Subject:  u.ID,
		TenantID: u.TenantID,
		Roles:    u.RolesList(),
		Name:     u.Username,
	}
	// 签发由 Signer 完成（非对称：仅持私钥的签发实例，验证方只持公钥）。
	token, err := b.signer.IssueToken(claims, ttl)
	if err != nil {
		auditLogin(ctx, c, u.TenantID, audit.ResultError, err.Error())
		return nil, fmt.Errorf("auth: issue token: %w", err)
	}
	auditLogin(ctx, c, u.TenantID, audit.ResultAllow, "")
	return &TokenPair{AccessToken: token, ExpiresAt: now.Add(ttl).Unix()}, nil
}

// queryUser 查询用户，经 retry + 熔断双层保护（Wave 1b/1c）。
//
// 分层语义（外层熔断、内层重试）：
//
//	Allow ──► [retry: 瞬时故障重试 N 次] ──► MarkSuccess/MarkFailure
//
//   - retry 处理**偶发抖动**（一次连接失败，重试可能成功）；
//   - 熔断处理**持续故障**（重试仍失败 → 计入失败；连续失败则 Open 快速失败，
//     不再让每个请求都付出 N 次重试的代价）。
//
// 熔断边界的关键设计——**只对真故障计数**：
//   - ErrNotFound（用户不存在）是**正常业务结果**，必须 MarkSuccess（不计失败），
//     否则攻击者用几个不存在的用户名就能熔断整个登录；
//   - 其余 error（DB 连接失败/超时等）才是真故障 → MarkFailure。
//
// 重试边界同理：分类器 RetryOnTransientDBError 排除 ErrNotFound（见其注释）。
//
// 为什么不用 cb.Execute：Execute 对 fn 的任何 error 一律 MarkFailure，
// 会把 NotFound 误计为失败。故手动 Allow/Mark 配对。
//
// 两个组件都为 nil 时直连 store（禁用态）。
func (b *Biz) queryUser(ctx context.Context, username string) (*authmodel.User, error) {
	q := func(ctx context.Context) (*authmodel.User, error) {
		return bootstrappkg.UserStore.Get(ctx, &store.Where{
			Filters: []*storev1.FilterCondition{store.Eq("username", username)},
		})
	}

	// 熔断前置检查（Open 时直接拒绝，不做重试）。
	if b.loginBreaker != nil {
		if err := b.loginBreaker.Allow(); err != nil {
			return nil, err // ErrCircuitOpen
		}
	}

	// 重试包裹实际查询（nil retrier 时单次执行）。
	u, err := b.queryWithRetry(ctx, q)

	// 熔断结果上报（只对真故障计失败）。
	if b.loginBreaker != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			b.loginBreaker.MarkFailure()
		} else {
			// 成功或 NotFound（正常业务结果）都算「依赖可用」。
			b.loginBreaker.MarkSuccess()
		}
	}
	return u, err
}

// queryWithRetry 执行查询并在瞬时故障时重试（retrier 为 nil 时单次执行）。
func (b *Biz) queryWithRetry(ctx context.Context,
	q func(context.Context) (*authmodel.User, error),
) (*authmodel.User, error) {
	if b.loginRetrier == nil {
		return q(ctx)
	}
	var (
		u   *authmodel.User
		err error
	)
	// Retrier.Do 的 fn 无返回值，经闭包捕获结果。
	rerr := b.loginRetrier.Do(ctx, func(c context.Context) error {
		u, err = q(c)
		return err
	})
	if rerr != nil {
		return nil, rerr
	}
	return u, nil
}

// auditLogin 记录登录审计事件（category=login，Object/Action 用 P9 归一化
// "auth"/"login"；IP/UA 经 Meta 传递由落库后端提取为独立列）。
func auditLogin(ctx context.Context, c Credential, tenantID string, result audit.Result, errMsg string) {
	ev := audit.AuditEvent{
		Time:     time.Now(),
		Subject:  c.Username, // 登录场景以账号名为主体（用户不存在时也是有效线索）
		TenantID: tenantID,
		Object:   "auth",
		Action:   "login",
		Result:   result,
		Error:    errMsg,
		Meta: map[string]any{
			"category":   "login",
			"client_ip":  c.ClientIP,
			"user_agent": c.UserAgent,
		},
	}
	recordSafely(ctx, ev)
}

// recordSafely 旁路记录：审计失败仅降级记日志，绝不影响登录主流程。
func recordSafely(ctx context.Context, ev audit.AuditEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn(ctx, "login audit panic recovered", "panic", r)
		}
	}()
	audit.GetAuditor().Record(ctx, ev)
}

// WhoAmI 从已认证上下文解析当前用户（subject 由 authn 中间件注入）。
func (b *Biz) WhoAmI(ctx context.Context) (*UserInfo, error) {
	claims := authn.AuthClaimsFromContext(ctx)
	if claims == nil {
		return nil, fmt.Errorf("auth: subject not found in context")
	}
	return &UserInfo{
		Username:  claims.Name,
		UserID:    claims.Subject,
		TenantID:  claims.TenantID,
		Roles:     claims.Roles,
		TokenType: "Bearer",
	}, nil
}
