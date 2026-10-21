package app

import (
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/token"
)

// buildTokenStore 构造令牌存储。
//
// 复用 bootstrap 已装配的 Redis 客户端（RedisClient，由 BeforeStart 的
// cache.redis 段构造）。无 Redis 时返回 nil——**禁用态**：
//   - Logout 记日志不吊销（无状态 JWT 无法收回）；
//   - Login 不签发 refresh_token（无处可查的刷新令牌没有意义）；
//   - ValidateToken 退化为纯验签（无吊销检查）。
//
// 这三条降级都在各自调用点显式处理，不静默。
//
// 登录防护三件构造器（buildLoginLimiter/Breaker/Retrier）住 auth 域声明包
// （internal/apiserver/domains/authd/config.go）——域 Wire 自读配置自构造，
// 测试见 authd/config_test.go。本函数是基础设施抉择（装配层产物），
// 域经 Deps getter 迟到取用。

func buildTokenStore() token.Store {
	if bootstrappkg.RedisClient == nil {
		return nil
	}
	return token.NewRedisStore(bootstrappkg.RedisClient)
}
