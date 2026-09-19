// Package token 提供认证令牌的**服务端状态**能力：吊销名单（Logout）与
// 刷新令牌存储（RefreshToken）。
//
// 为什么需要本包：JWT 是**无状态**的——签发后服务端不再持有它，验签通过即放行。
// 这带来两个缺口：
//   - 登出：无法「收回」已签发的 token（除非等它自然过期）；
//   - 刷新：refresh_token 需要服务端可查（否则等同长期 access_token）。
//
// 本包用 Redis 承载这两块状态，并通过 RevocationChecker 装饰器把吊销检查接入
// 认证链路——**框架零改动**（bald 的 AuthnMiddleware 只依赖 authn.Authenticator
// 接口，装饰器是同一接口的合法实现）。
//
// 框架能力验证结论（Wave 1d 实测）：bald v0.8.1 **不提供** session/token 管理
// 组件（`grep -rl "captcha\|session" --include="*.go" bald/` 零命中），故本包为
// 业务自建——这是「能力轴缺口」的一条实测记录。
//
// 依赖方向：本包只依赖 bald 的 authn 接口与 cache 接口，**不依赖 bootstrap**
// （bootstrap 依赖本包以装配），避免循环。
package token

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/kalandramo/bald/pkg/authn"
	goredis "github.com/redis/go-redis/v9"
)

// ErrRevoked 令牌已被吊销（登出/管理员拉黑）。
var ErrRevoked = errors.New("token: revoked")

// Store 是令牌服务端状态的存储抽象。Redis 为生产实现；测试可注入内存实现。
//
// 接口刻意收窄到三个操作——只覆盖 Logout/RefreshToken 的真实需要，不做通用 KV。
type Store interface {
	// Revoke 把 token 加入吊销名单，ttl 为记录的存活时间。
	// **ttl 应取 token 的剩余有效期**：过期 token 本就无效，记录长期驻留只是浪费内存。
	Revoke(ctx context.Context, token string, ttl time.Duration) error

	// IsRevoked 查询 token 是否在吊销名单中。
	IsRevoked(ctx context.Context, token string) (bool, error)

	// SaveRefresh 保存刷新令牌到 subject 的映射（ttl 为刷新令牌有效期）。
	SaveRefresh(ctx context.Context, refreshToken, subject string, ttl time.Duration) error

	// ConsumeRefresh 读取并**删除**刷新令牌（一次性语义：刷新后旧的作废，
	// 防止同一 refresh_token 被重放）。返回关联的 subject；不存在返回 ErrNotFound。
	ConsumeRefresh(ctx context.Context, refreshToken string) (string, error)
}

// ErrNotFound 刷新令牌不存在或已过期。
var ErrNotFound = errors.New("token: refresh token not found")

// fingerprint 把 token 转成固定长度的存储键指纹。
//
// 为什么存指纹而非原文：token 是**凭证**——把它以明文写进 Redis，等于在缓存层
// 复制了一份可用的登录凭据（Redis 若泄漏/被 dump，攻击者可直接冒用）。SHA-256
// 单向，泄漏也无法反推 token。
//
// 用 SHA-256 而非 bcrypt：本处只需「同 token 得同键」的确定性映射，不需要抗暴力
// 破解的慢哈希（token 本身是高熵随机串，且查表在认证热路径上，慢哈希会拖垮性能）。
func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// keyRevoked / keyRefresh 是 Redis 键前缀（命名空间隔离，避免与其他业务键碰撞）。
func keyRevoked(token string) string { return "auth:revoked:" + fingerprint(token) }
func keyRefresh(token string) string { return "auth:refresh:" + fingerprint(token) }

// RedisStore 是基于 Redis 的 Store 实现。
//
// 用 UniversalClient 而非 cache.Cache 适配器：刷新令牌需要**原子读取并删除**
// （ConsumeRefresh 的一次性语义），而 cache.Cache 接口的 Get+Delete 两步之间
// 存在竞态窗口（并发刷新同一 token 可能都被放行）。Redis 的 GETDEL 是原子操作。
// 这是「适配器接口够不够用」的一条实测结论。
//
// 直接依赖 go-redis 的 UniversalClient（而非自定义窄接口）：go-redis 的方法
// 返回 *StatusCmd/*IntCmd/*StringCmd 等命令对象（需 .Result()/.Err() 取值），
// 自定义「返回 (T, error)」的接口无法被 *goredis.Client 满足——写适配器只是
// 多一层无收益的转发。go-redis 已在依赖树中（bootstrap 使用），不新增依赖。
type RedisStore struct {
	rdb goredis.UniversalClient
}

// NewRedisStore 构造 Redis 令牌存储。
func NewRedisStore(rdb goredis.UniversalClient) *RedisStore { return &RedisStore{rdb: rdb} }

// Revoke 实现 Store。
func (s *RedisStore) Revoke(ctx context.Context, token string, ttl time.Duration) error {
	if token == "" {
		return errors.New("token: empty token")
	}
	if ttl <= 0 {
		// 已过期或未指定：无需拉黑（过期 token 验签本就会失败）。
		return nil
	}
	if err := s.rdb.Set(ctx, keyRevoked(token), "1", ttl).Err(); err != nil {
		return fmt.Errorf("token: revoke: %w", err)
	}
	return nil
}

// IsRevoked 实现 Store。
func (s *RedisStore) IsRevoked(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	n, err := s.rdb.Exists(ctx, keyRevoked(token)).Result()
	if err != nil {
		return false, fmt.Errorf("token: is revoked: %w", err)
	}
	return n > 0, nil
}

// SaveRefresh 实现 Store。
func (s *RedisStore) SaveRefresh(ctx context.Context, refreshToken, subject string, ttl time.Duration) error {
	if refreshToken == "" || subject == "" {
		return errors.New("token: empty refresh token or subject")
	}
	if err := s.rdb.Set(ctx, keyRefresh(refreshToken), subject, ttl).Err(); err != nil {
		return fmt.Errorf("token: save refresh: %w", err)
	}
	return nil
}

// ConsumeRefresh 实现 Store：原子 GETDEL——读取即删除，杜绝重放竞态。
func (s *RedisStore) ConsumeRefresh(ctx context.Context, refreshToken string) (string, error) {
	if refreshToken == "" {
		return "", ErrNotFound
	}
	subject, err := s.rdb.GetDel(ctx, keyRefresh(refreshToken)).Result()
	if err != nil {
		// go-redis 的 redis.Nil 表示键不存在——归一为本包 ErrNotFound，
		// 让调用方不必依赖具体 Redis 客户端库的错误类型。
		return "", ErrNotFound
	}
	if subject == "" {
		return "", ErrNotFound
	}
	return subject, nil
}

// RevocationChecker 是 authn.Authenticator 的**装饰器**：先做验签（委托给被包装
// 的认证器），验签通过后再查吊销名单。被吊销则返回 ErrRevoked。
//
// 为什么用装饰器而非改中间件：bald 的 AuthnMiddleware 只依赖 authn.Authenticator
// 接口，装饰器是同一接口的合法实现——**框架零改动**即可把吊销检查接入认证链路。
// 这同时验证了框架的「接口可组合性」：认证策略可被业务包装而不侵入核心。
//
// 顺序很关键：**先验签、后查名单**。
//   - 先验签：伪造的 token 直接失败，不会命中 Redis（避免被垃圾 token 打爆缓存）；
//   - 后查名单：只需对**验签通过**的真 token 查一次 Redis。
type RevocationChecker struct {
	inner authn.Authenticator
	store Store
}

// NewRevocationChecker 包装认证器，加入吊销检查。store 为 nil 时直接返回 inner
// （禁用态——与 Wave 1a/1b/1c 的 nil 即禁用语义一致）。
func NewRevocationChecker(inner authn.Authenticator, store Store) authn.Authenticator {
	if store == nil {
		return inner
	}
	return &RevocationChecker{inner: inner, store: store}
}

// Authenticate 实现 authn.Authenticator：验签 → 查吊销名单。
func (c *RevocationChecker) Authenticate(ctx context.Context) (*authn.AuthClaims, error) {
	claims, err := c.inner.Authenticate(ctx)
	if err != nil {
		return nil, err // 验签失败：不查 Redis
	}
	tok := authn.TokenFromContext(ctx)
	if revoked, rerr := c.store.IsRevoked(ctx, tok); rerr != nil {
		// Redis 故障：**fail-closed**（拒绝）而非放行。
		// 理由：吊销检查是安全边界——放行意味着「登出可能不生效」，
		// 攻击者可用 Redis 故障绕过登出。安全能力故障时应拒绝，不是放行。
		return nil, fmt.Errorf("token: revocation check failed: %w", rerr)
	} else if revoked {
		return nil, ErrRevoked
	}
	return claims, nil
}

// AuthenticateToken 实现 authn.Authenticator：直接校验 token 字符串（非 HTTP 场景）。
func (c *RevocationChecker) AuthenticateToken(token string) (*authn.AuthClaims, error) {
	claims, err := c.inner.AuthenticateToken(token)
	if err != nil {
		return nil, err
	}
	if revoked, rerr := c.store.IsRevoked(context.Background(), token); rerr != nil {
		return nil, fmt.Errorf("token: revocation check failed: %w", rerr)
	} else if revoked {
		return nil, ErrRevoked
	}
	return claims, nil
}
