package app

import (
	"context"
	"time"

	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/token"

	baldconfig "github.com/kalandramo/bald/bootstrap/config"
	"github.com/kalandramo/bald/circuitbreaker"
	"github.com/kalandramo/bald/circuitbreaker/hystrix"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/ratelimit"
	"github.com/kalandramo/bald/ratelimit/tokenbucket"
	"github.com/kalandramo/bald/retry"
)

func buildLoginLimiter(cfg *baldconfig.Store) ratelimit.Limiter {
	rate := configFloat(cfg, "login.rate_limit.rate")
	burst := configFloat(cfg, "login.rate_limit.burst")
	if rate <= 0 || burst <= 0 {
		return nil // 未配置或配置不全：禁用态
	}
	lim, err := tokenbucket.New(rate, burst)
	if err != nil {
		baldlog.Warn(context.Background(), "login rate limiter disabled: invalid config",
			"rate", rate, "burst", burst, "error", err.Error())
		return nil
	}
	baldlog.Info(context.Background(), "login rate limiter enabled", "rate", rate, "burst", burst)
	return lim
}

// buildLoginBreaker 从业务自持配置段构造登录 DB 查询熔断器（Wave 1b）。
//
// 配置键：
//
//	login:
//	  breaker:
//	    error_threshold: 0.5        # 错误率阈值（0-1），default 0.5
//	    request_volume: 20          # 最小请求量（低于此不评估），default 20
//	    sleep_window_seconds: 5     # Open 后多久转半开（秒），default 5
//
// 全部字段缺省时返回 nil（禁用态）——熔断是防御性增强，未配置则不介入。
// 用 sres 之外的 **hystrix**（阈值式）：sres 的概率式语义不符合契约对
// StateClosed 的定义（详见 Wave 1b 交付报告与 e2e 测试注释）。

func buildLoginBreaker(cfg *baldconfig.Store) circuitbreaker.CircuitBreaker {
	_, configured := cfg.Get("login.breaker")
	if !configured {
		return nil // 未配置：禁用态
	}
	opts := []hystrix.Option{}
	if v := configFloat(cfg, "login.breaker.error_threshold"); v > 0 {
		opts = append(opts, hystrix.WithErrorThreshold(v))
	}
	if v := configFloat(cfg, "login.breaker.request_volume"); v > 0 {
		opts = append(opts, hystrix.WithRequestVolumeThreshold(int(v)))
	}
	if v := configFloat(cfg, "login.breaker.sleep_window_seconds"); v > 0 {
		opts = append(opts, hystrix.WithSleepWindow(time.Duration(v)*time.Second))
	}
	cb := hystrix.New(opts...)
	baldlog.Info(context.Background(), "login breaker enabled")
	return cb
}

// buildLoginRetrier 从业务自持配置段构造登录 DB 查询重试器（Wave 1c）。
//
// 配置键：
//
//	login:
//	  retry:
//	    max_attempts: 3             # 最大尝试次数（含首次），default 3
//	    initial_backoff_ms: 100     # 首次退避（毫秒），default 200
//	    max_backoff_ms: 2000        # 退避上限（毫秒），default 10000
//
// 整段缺省时返回 nil（禁用态，单次查询）。
// 分类器固定为 authbiz.RetryOnTransientDBError——**只重试瞬时故障**，
// 不重试 ErrNotFound（确定性业务结果，重试只是浪费 DB 往返）。

func buildLoginRetrier(cfg *baldconfig.Store) *retry.Retrier {
	if _, configured := cfg.Get("login.retry"); !configured {
		return nil // 未配置：禁用态
	}
	opts := []retry.Option{
		retry.WithClassifier(authbiz.RetryOnTransientDBError),
	}
	if v := configFloat(cfg, "login.retry.max_attempts"); v >= 1 {
		opts = append(opts, retry.WithMaxAttempts(int(v)))
	}
	backoff := retry.ExponentialBackoff{
		Initial: 200 * time.Millisecond,
		Factor:  2,
		Max:     10 * time.Second,
	}
	if v := configFloat(cfg, "login.retry.initial_backoff_ms"); v > 0 {
		backoff.Initial = time.Duration(v) * time.Millisecond
	}
	if v := configFloat(cfg, "login.retry.max_backoff_ms"); v > 0 {
		backoff.Max = time.Duration(v) * time.Millisecond
	}
	opts = append(opts, retry.WithBackoff(backoff), retry.WithJitter(retry.FullJitter))
	r := retry.New(opts...)
	baldlog.Info(context.Background(), "login retrier enabled",
		"initial_backoff_ms", backoff.Initial.Milliseconds(), "max_backoff_ms", backoff.Max.Milliseconds())
	return r
}

// buildTokenStore 构造令牌存储（Wave 1d）。
//
// 复用 bootstrap 已装配的 Redis 客户端（RedisClient，由 BeforeStart 的
// cache.redis 段构造）。无 Redis 时返回 nil——**禁用态**：
//   - Logout 记日志不吊销（无状态 JWT 无法收回）；
//   - Login 不签发 refresh_token（无处可查的刷新令牌没有意义）；
//   - ValidateToken 退化为纯验签（无吊销检查）。
//
// 这三条降级都在各自调用点显式处理，不静默。

func buildTokenStore() token.Store {
	if bootstrappkg.RedisClient == nil {
		return nil
	}
	return token.NewRedisStore(bootstrappkg.RedisClient)
}

// configFloat 从配置树取数值键（Store 无 GetFloat，经 Get + 类型断言；
// yaml 解析的整数会落为 int，故两种数值类型都接受）。

func configFloat(cfg *baldconfig.Store, key string) float64 {
	v, ok := cfg.Get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
