// Package auth 实现 M1 认证授权范本的业务逻辑（biz 层）。
//
// 本层不依赖 gin/grpc：业务函数以纯 Go 入参/出参呈现，由 handler 层负责协议
// 转换（HTTP/gRPC transcoding）。这是 osbuilder/bald 推荐的分层：
// handler（协议）→ biz（业务）→ store（数据）。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
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
	"github.com/kalandramo/bald-admin/internal/security/token"
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
	// RefreshToken 刷新令牌（Wave 1d）。仅在令牌存储可用时签发——空串表示
	// 无刷新能力（禁用态），客户端应降级为「过期后重新登录」。
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenType 令牌类型（对齐源 LoginResponse.token_type，恒为 "Bearer"）。
	TokenType string `json:"token_type,omitempty"`
}

// ValidateResult 是 ValidateToken 的返回（Wave 1d）。
// 语义是「询问」而非「准入」：无效 token 返回 Valid=false，**不是错误码**——
// 调用方（如网关）需要区分「token 无效」（业务结果）与「校验服务故障」。
type ValidateResult struct {
	Valid     bool   `json:"valid"`
	Subject   string `json:"subject,omitempty"`
	Username  string `json:"username,omitempty"`
	TenantID  string `json:"tenant_id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Reason    string `json:"reason,omitempty"` // 无效原因（valid=false 时）
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
	// tokenStore 令牌服务端状态存储（Wave 1d：吊销名单 + 刷新令牌）。
	// nil = 禁用态：Logout 记日志不吊销（无法收回无状态 JWT）、不签发刷新令牌。
	tokenStore token.Store
	// authenticator 令牌校验器（Wave 1d：ValidateToken 需要验签能力）。
	// 与 signer 分离——签发持私钥、验签只需公钥（非对称解耦）。
	// 经 setter 注入（bootstrap 的 LazyAuthenticator，请求期解析）。
	authenticator authn.Authenticator
	// accessTTL / refreshTTL 令牌有效期（Wave 1d）。零值用默认。
	accessTTL  time.Duration
	refreshTTL time.Duration
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

// SetTokenStore 运行期注入令牌存储（Wave 1d）。nil 不覆盖——保留禁用态语义。
// 与 SetLoginLimiter 同款的运行期注入（存储在 BeforeStart 装配 Redis 后才就绪）。
func (b *Biz) SetTokenStore(s token.Store) {
	if s != nil {
		b.tokenStore = s
	}
}

// SetAuthenticator 运行期注入令牌校验器（Wave 1d，ValidateToken 用）。
// nil 不覆盖——ValidateToken 将退化为「无法校验」错误。
func (b *Biz) SetAuthenticator(a authn.Authenticator) {
	if a != nil {
		b.authenticator = a
	}
}

// SetTokenTTL 设置访问/刷新令牌有效期（Wave 1d）。零值忽略，用默认。
func (b *Biz) SetTokenTTL(access, refresh time.Duration) {
	if access > 0 {
		b.accessTTL = access
	}
	if refresh > 0 {
		b.refreshTTL = refresh
	}
}

// accessTokenTTL / refreshTokenTTL 返回生效的 TTL（未设置用默认）。
// 默认 access 2h（与 Wave 1d 之前的硬编码一致，行为零回归）；
// refresh 7d（源 go-wind-admin 的刷新令牌为长有效期）。
func (b *Biz) accessTokenTTL() time.Duration {
	if b.accessTTL > 0 {
		return b.accessTTL
	}
	return 2 * time.Hour
}

func (b *Biz) refreshTokenTTL() time.Duration {
	if b.refreshTTL > 0 {
		return b.refreshTTL
	}
	return 7 * 24 * time.Hour
}

// ErrRefreshInvalid 刷新令牌无效（不存在/已过期/已使用）。
var ErrRefreshInvalid = errors.New("auth: invalid refresh token")

// ErrTokenInvalid 令牌无效（ValidateToken 场景，非错误码）。
var ErrTokenInvalid = errors.New("auth: invalid token")

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
	ttl := b.accessTokenTTL()
	claims := authn.AuthClaims{
		Issuer:   "go-bald-admin",
		Subject:  u.ID,
		TenantID: u.TenantID,
		Roles:    u.RolesList(),
		Name:     u.Username,
		// nonce scope：绕开框架「同秒同 claims 产生相同 token」的缺陷（见 newNonce）。
		// 无它则同一秒内两次登录拿到同一 token——登出其一即吊销两者。
		Scopes: []string{"nonce:" + newNonce()},
	}
	// 签发由 Signer 完成（非对称：仅持私钥的签发实例，验证方只持公钥）。
	token, err := b.signer.IssueToken(claims, ttl)
	if err != nil {
		auditLogin(ctx, c, u.TenantID, audit.ResultError, err.Error())
		return nil, fmt.Errorf("auth: issue token: %w", err)
	}
	auditLogin(ctx, c, u.TenantID, audit.ResultAllow, "")

	pair := &TokenPair{
		AccessToken: token,
		ExpiresAt:   now.Add(ttl).Unix(),
		TokenType:   "Bearer",
	}
	// Wave 1d：刷新令牌（仅令牌存储可用时签发）。
	// 无存储则刷新令牌无处可查，签发等于给了客户端一个用不了的东西——
	// 故留空（客户端据 refresh_token 缺失降级为重新登录）。
	if b.tokenStore != nil {
		rt, rerr := b.issueRefresh(ctx, u.ID)
		if rerr != nil {
			// 刷新令牌签发失败不应阻断登录本身（访问令牌已可用）——
			// 记日志降级，用户仍能正常使用，只是不能刷新。
			log.Warn(ctx, "issue refresh token failed, degrading", "error", rerr.Error())
		} else {
			pair.RefreshToken = rt
		}
	}
	return pair, nil
}

// issueRefresh 签发并登记刷新令牌。刷新令牌用 Signer 签发（带 subject + 长 TTL），
// 同时把「令牌 → subject」写入存储——刷新时必须能在存储中查到，否则等同长期
// access_token（无法撤销）。
func (b *Biz) issueRefresh(ctx context.Context, subject string) (string, error) {
	ttl := b.refreshTokenTTL()
	rt, err := b.signer.IssueToken(authn.AuthClaims{
		Issuer:  "go-bald-admin",
		Subject: subject,
		// nonce scope：**关键**——刷新令牌是一次性的（ConsumeRefresh 用 GETDEL
		// 读取即删）。若轮换时签发出与旧令牌**字节相同**的新令牌，则旧的刚被删除、
		// 新的又与它同值，等于「删除后又存在」，一次性语义被破坏（旧令牌仍可用）。
		// 根因见 newNonce 注释（框架不设 jti）。
		Scopes: []string{"nonce:" + newNonce()},
	}, ttl)
	if err != nil {
		return "", fmt.Errorf("auth: issue refresh token: %w", err)
	}
	if err := b.tokenStore.SaveRefresh(ctx, rt, subject, ttl); err != nil {
		return "", fmt.Errorf("auth: save refresh token: %w", err)
	}
	return rt, nil
}

// newNonce 生成 128 位随机 nonce，用作签发时的唯一性来源。
//
// **框架缺陷绕行**（Wave 1d 实测，待上游确认）：
// bald/contrib/authn-jwt 的 toJWT（jwt.go:208-221）不设置 RegisteredClaims.ID
//（即 JWT 标准 jti），且 iat/nbf/exp 均为**秒级** NumericDate（jwt.go:298-305）。
// 在 RSA 签名确定性（RS256 对同一输入恒等）的前提下，**同一秒内对相同 claims
// 签发会得到字节完全相同的 token**。
//
// 后果（本会话实测）：快速连续刷新时新 access_token 与旧的相同（有效期不延展，
// 客户端拿到的「新」令牌立刻过期）；更严重的是轮换出的 refresh_token 可能与
// 刚被消费的旧值重合，使一次性语义失效。
//
// 应用层绕行：把 nonce 放进 Scopes（唯一可由业务控制的字段），让每次签发
// claims 不同。**这不能修框架**——只是让业务在框架缺陷下仍正确工作。
// 正确修复应在上游给 toJWT 填 ID（jti = 随机 UUID），届时本绕行可移除。
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极罕见（系统熵源故障）；退化为纳秒时间戳保证唯一性
		// 仍优于返回相同 token。
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// Logout 登出（Wave 1d，对应源 authentication.proto L28）。
//
// 难点：JWT 无状态——签发后服务端不再持有它，无法「收回」。唯一的办法是把
// token 加入**吊销名单**，并让认证链路查名单（由 token.RevocationChecker
// 装饰器完成，框架零改动）。
//
// 幂等：重复登出同一 token 返回成功（第二次只是又拉黑一次，语义无害）。
//
// 降级：tokenStore 为 nil 时记日志并返回成功——但**吊销能力缺失**是已知降级
// （无状态 JWT 无法收回），故显式记 Warn 而非静默。
func (b *Biz) Logout(ctx context.Context) error {
	tok := authn.TokenFromContext(ctx)
	claims := authn.AuthClaimsFromContext(ctx)
	subject := ""
	if claims != nil {
		subject = claims.Subject
	}
	auditLogout(ctx, subject, tok, audit.ResultAllow, "")

	if b.tokenStore == nil {
		log.Warn(ctx, "logout without token store: token cannot be revoked (stateless JWT)")
		return nil
	}
	if tok == "" {
		return nil // 无 token 可拉黑（中间件已保证有 token，防御性）
	}
	// TTL 取 token 剩余有效期：过期 token 本就无效，拉黑记录长期驻留只是浪费内存。
	ttl := remainingTTL(claims)
	if err := b.tokenStore.Revoke(ctx, tok, ttl); err != nil {
		auditLogout(ctx, subject, tok, audit.ResultError, err.Error())
		return fmt.Errorf("auth: revoke token: %w", err)
	}
	return nil
}

// remainingTTL 计算 token 的剩余有效期（用于拉黑记录 TTL）。
// claims 缺失或已过期时返回 0（无需拉黑）。
func remainingTTL(claims *authn.AuthClaims) time.Duration {
	if claims == nil || claims.ExpiresAt.IsZero() {
		return 0
	}
	d := time.Until(claims.ExpiresAt)
	if d < 0 {
		return 0
	}
	return d
}

// RefreshToken 用刷新令牌换发新令牌对（Wave 1d，对应源 L35）。
//
// **一次性语义**：ConsumeRefresh 读取即删除——旧刷新令牌立即作废，防止同一
// refresh_token 被重放（若可重复使用，它就成了永久凭证，刷新机制形同虚设）。
func (b *Biz) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	if b.tokenStore == nil {
		return nil, ErrRefreshInvalid // 无存储：刷新能力未启用
	}
	if refreshToken == "" {
		return nil, ErrRefreshInvalid
	}
	// 原子消费：读取即删除（并发刷新同一 token 只有一个能成功）。
	subject, err := b.tokenStore.ConsumeRefresh(ctx, refreshToken)
	if err != nil {
		auditRefresh(ctx, "", audit.ResultDeny, "invalid refresh token")
		return nil, ErrRefreshInvalid
	}

	// 重新加载用户：刷新时用户可能已被删除/停用/改角色——直接用旧 claims 会
	// 让权限变更在刷新后仍不生效（刷新本应是最新的权限快照）。
	u, err := bootstrappkg.UserStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", subject)},
	})
	if err != nil {
		auditRefresh(ctx, subject, audit.ResultError, err.Error())
		return nil, fmt.Errorf("auth: load user for refresh: %w", err)
	}

	now := time.Now()
	ttl := b.accessTokenTTL()
	access, err := b.signer.IssueToken(authn.AuthClaims{
		Issuer:   "go-bald-admin",
		Subject:  u.ID,
		TenantID: u.TenantID,
		Roles:    u.RolesList(),
		Name:     u.Username,
		// nonce scope：绕开框架的「同秒同 claims 产生相同 token」缺陷（见下）。
		Scopes: []string{"nonce:" + newNonce()},
	}, ttl)
	if err != nil {
		auditRefresh(ctx, subject, audit.ResultError, err.Error())
		return nil, fmt.Errorf("auth: issue access token: %w", err)
	}
	// 换发新的刷新令牌（旧的在 ConsumeRefresh 已作废——轮换语义）。
	newRefresh, err := b.issueRefresh(ctx, u.ID)
	if err != nil {
		log.Warn(ctx, "issue rotated refresh token failed, degrading", "error", err.Error())
	}
	auditRefresh(ctx, subject, audit.ResultAllow, "")
	return &TokenPair{
		AccessToken:  access,
		ExpiresAt:    now.Add(ttl).Unix(),
		RefreshToken: newRefresh,
		TokenType:    "Bearer",
	}, nil
}

// ValidateToken 校验令牌有效性（Wave 1d，对应源 L38）。
//
// 语义是「询问」而非「准入」：无效令牌返回 Valid=false，**不是错误码**。
// 调用方（如网关）需要区分「令牌无效」（业务结果）与「校验服务故障」——
// 返回错误码会让两者混淆。
//
// 校验包含吊销检查：登出后的令牌必须报 invalid（与认证中间件同源语义）。
func (b *Biz) ValidateToken(ctx context.Context, token string) *ValidateResult {
	if b.authenticator == nil {
		return &ValidateResult{Valid: false, Reason: "validator not configured"}
	}
	claims, err := b.authenticator.AuthenticateToken(token)
	if err != nil {
		return &ValidateResult{Valid: false, Reason: err.Error()}
	}
	// 吊销检查（tokenStore 可用时）。
	if b.tokenStore != nil {
		if revoked, rerr := b.tokenStore.IsRevoked(ctx, token); rerr == nil && revoked {
			return &ValidateResult{Valid: false, Reason: "token revoked"}
		}
	}
	res := &ValidateResult{
		Valid:    true,
		Subject:  claims.Subject,
		Username: claims.Name,
		TenantID: claims.TenantID,
	}
	if !claims.ExpiresAt.IsZero() {
		res.ExpiresAt = claims.ExpiresAt.Unix()
	}
	return res
}

// auditLogout / auditRefresh 记录认证生命周期审计（与 auditLogin 同构）。
func auditLogout(ctx context.Context, subject, tok string, result audit.Result, errMsg string) {
	recordSafely(ctx, audit.AuditEvent{
		Time:    time.Now(),
		Subject: subject,
		Object:  "auth",
		Action:  "logout",
		Result:  result,
		Error:   errMsg,
		Meta:    map[string]any{"category": "logout", "token_fp": tokenFingerprint(tok)},
	})
}

func auditRefresh(ctx context.Context, subject string, result audit.Result, errMsg string) {
	recordSafely(ctx, audit.AuditEvent{
		Time:    time.Now(),
		Subject: subject,
		Object:  "auth",
		Action:  "refresh",
		Result:  result,
		Error:   errMsg,
		Meta:    map[string]any{"category": "refresh"},
	})
}

// tokenFingerprint 取 token 的短指纹（审计留痕用，**不落 token 明文**——
// 审计日志可能被更多人看到，明文 token 等于泄漏凭证）。
func tokenFingerprint(tok string) string {
	if tok == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:8]) // 前 8 字节足够区分，且不可反推
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
