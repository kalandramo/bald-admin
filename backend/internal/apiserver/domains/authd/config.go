// config.go —— auth 域自持的登录防护配置（biz.login.*）与三件构造器。
//
// 内容三件：
//   - 配置类型/默认值/校验/严格解码；
//   - 限流/熔断/重试构造器：buildLoginLimiter/Breaker/Retrier
//     （含「>0 才覆盖」「段存在即启用、缺省即禁用」语义）；
//   - 命令行 flag 声明：AddFlags 的 biz.login.* 段，经 PlainBinder
//     由装配层 Bind——键名与默认值仍归本包，flag 值经配置合并层进入 Settings
//     快照，域 Wire 解码时与文件/env 同源同优先级。
package authd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"

	"github.com/kalandramo/bald/circuitbreaker"
	"github.com/kalandramo/bald/circuitbreaker/hystrix"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/ratelimit"
	"github.com/kalandramo/bald/ratelimit/tokenbucket"
	"github.com/kalandramo/bald/retry"

	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
)

// ConfigSection 是本域在业务配置命名空间（biz）下的自持段名。
//
// 装配层以此把该子树从自己的严格解码（ServerOptions.DecodeInto）中排除——
// 段的解码与校验归本包（decodeLogin），装配层不再出现业务阈值。
const ConfigSection = "login"

// LoginConfig 登录防护三件套（限流 / 熔断 / 重试）。
//
// Breaker / Retry 用**指针**表达「整段配置是否存在」：契约语义是「段存在即启用、
// 缺省即禁用」（防御性增强，未配置不介入）。指针在 mapstructure 解码时对缺失的
// 段保持 nil，故能精确保留该语义。
type LoginConfig struct {
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	// Breaker 非 nil = 启用登录 DB 查询熔断（段存在即启用）。
	Breaker *BreakerConfig `mapstructure:"breaker"`
	// Retry 非 nil = 启用登录 DB 查询重试（段存在即启用）。
	Retry *RetryConfig `mapstructure:"retry"`
}

// RateLimitConfig 登录限流（令牌桶）。零值=禁用（fail-open，不阻断登录）。
type RateLimitConfig struct {
	Rate  float64 `mapstructure:"rate"`
	Burst float64 `mapstructure:"burst"`
}

// BreakerConfig 登录 DB 查询熔断（阈值式 hystrix）。
type BreakerConfig struct {
	ErrorThreshold     float64 `mapstructure:"error_threshold"`
	RequestVolume      int     `mapstructure:"request_volume"`
	SleepWindowSeconds int     `mapstructure:"sleep_window_seconds"`
}

// RetryConfig 登录 DB 查询重试（瞬时故障）。
type RetryConfig struct {
	MaxAttempts      int `mapstructure:"max_attempts"`
	InitialBackoffMs int `mapstructure:"initial_backoff_ms"`
	MaxBackoffMs     int `mapstructure:"max_backoff_ms"`
}

// breakerDefaults / retryDefaults 返回启用态下的字段缺省值：flag 默认值展示与
// nil 段校验兜底用；启用态构造时字段值由「>0 才覆盖」落到库默认（见 builders）。
func breakerDefaults() BreakerConfig {
	return BreakerConfig{ErrorThreshold: 0.5, RequestVolume: 20, SleepWindowSeconds: 5}
}

func retryDefaults() RetryConfig {
	return RetryConfig{MaxAttempts: 3, InitialBackoffMs: 200, MaxBackoffMs: 10000}
}

// BreakerEnabled / RetryEnabled 报告该能力是否被配置启用（段是否存在）。
func (c LoginConfig) BreakerEnabled() bool { return c.Breaker != nil }
func (c LoginConfig) RetryEnabled() bool   { return c.Retry != nil }

// BreakerOrDefault 返回熔断配置：未配置（nil）时返回缺省值。
func (c LoginConfig) BreakerOrDefault() BreakerConfig {
	if c.Breaker == nil {
		return breakerDefaults()
	}
	return *c.Breaker
}

// RetryOrDefault 返回重试配置：未配置（nil）时返回缺省值。
func (c LoginConfig) RetryOrDefault() RetryConfig {
	if c.Retry == nil {
		return retryDefaults()
	}
	return *c.Retry
}

// decodeLogin 从 Settings 快照解码 biz.login 段并校验（段内严格：未知键报错，
// 业务键笔误启动期暴露而非静默落默认值）。段缺失合法（全字段零值=禁用态）。
func decodeLogin(settings map[string]any) (LoginConfig, error) {
	var raw map[string]any
	if settings != nil {
		if b, ok := settings["biz"].(map[string]any); ok {
			if v, ok := b[ConfigSection]; ok && v != nil {
				m, ok := v.(map[string]any)
				if !ok {
					return LoginConfig{}, fmt.Errorf("authd: biz.%s must be a map, got %T", ConfigSection, v)
				}
				raw = m
			}
		}
	}
	var cfg LoginConfig
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &cfg,
		TagName:          "mapstructure",
		WeaklyTypedInput: true, // yaml/flag 值多为 string/int，宽松转换到目标类型
		// 只解 login 子树——未知键即本域配置键笔误，报错。
		ErrorUnused: true,
	})
	if err != nil {
		return cfg, fmt.Errorf("authd: build login decoder: %w", err)
	}
	if err := dec.Decode(raw); err != nil {
		return cfg, fmt.Errorf("authd: decode biz.%s section: %w", ConfigSection, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate 校验配置合法性，**聚合**全部错误后一次性返回（原 options.Validate
// 的 login 行逐字迁入）。
func (c LoginConfig) Validate() error {
	var errs []string

	// 限流：0=禁用合法；负值非法（无意义且掩盖配置笔误）。
	if c.RateLimit.Rate < 0 {
		errs = append(errs, fmt.Sprintf("login.rate_limit.rate must be >= 0 (got %v)", c.RateLimit.Rate))
	}
	if c.RateLimit.Burst < 0 {
		errs = append(errs, fmt.Sprintf("login.rate_limit.burst must be >= 0 (got %v)", c.RateLimit.Burst))
	}
	// 熔断：仅在启用（段存在）时校验；未配置即禁用，不产生错误。
	if br := c.BreakerOrDefault(); br.ErrorThreshold < 0 || br.ErrorThreshold > 1 {
		errs = append(errs, fmt.Sprintf("login.breaker.error_threshold must be within [0,1] (got %v)", br.ErrorThreshold))
	} else if c.BreakerEnabled() {
		if br.RequestVolume <= 0 {
			errs = append(errs, fmt.Sprintf("login.breaker.request_volume must be > 0 (got %d)", br.RequestVolume))
		}
		if br.SleepWindowSeconds <= 0 {
			errs = append(errs, fmt.Sprintf("login.breaker.sleep_window_seconds must be > 0 (got %d)", br.SleepWindowSeconds))
		}
	}
	// 重试：次数至少 1（含首次）；仅在启用时校验。
	if c.RetryEnabled() {
		if rt := c.RetryOrDefault(); rt.MaxAttempts < 1 {
			errs = append(errs, fmt.Sprintf("login.retry.max_attempts must be >= 1 (got %d)", rt.MaxAttempts))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid login options: %s", strings.Join(errs, "; "))
	}
	return nil
}

// FlagOptions 是 biz.login.* 的命令行 flag 声明（appkit.PlainBinder 形态）。
//
// 装配层经 appkit.Bind("", &authd.FlagOptions{}) 声明进装载 FlagSet——键名与
// 默认值住本包（业务阈值不出域），flag 值经 flattenFlags 进配置合并（仅
// Changed 的 flag 参与，优先级 flag > env > 文件）。熔断/重试仍绑**影子字段**
// 而非 LoginConfig 的指针：pflag 绑定会立刻 materialize 指针，「段缺省」会变
// 「段存在」静默启用（实测复现过的缺陷，影子纪律随域迁移保持）；「段存在」
// 与否由域 Wire 的解码按配置内容裁定。
type FlagOptions struct {
	rateLimitRate             float64
	rateLimitBurst            float64
	breakerErrorThreshold     float64
	breakerRequestVolume      int
	breakerSleepWindowSeconds int
	retryMaxAttempts          int
	retryInitialBackoffMs     int
	retryMaxBackoffMs         int
}

// AddFlags 实现 appkit.PlainBinder：注册 --biz.login.* 八个旗标。
func (f *FlagOptions) AddFlags(fs *pflag.FlagSet) {
	fs.Float64Var(&f.rateLimitRate, "biz.login.rate_limit.rate", f.rateLimitRate,
		"Login rate limit: tokens per second (<=0 disables).")
	fs.Float64Var(&f.rateLimitBurst, "biz.login.rate_limit.burst", f.rateLimitBurst,
		"Login rate limit: burst capacity (<=0 disables).")

	br := breakerDefaults()
	rt := retryDefaults()
	fs.Float64Var(&f.breakerErrorThreshold, "biz.login.breaker.error_threshold", br.ErrorThreshold,
		"Login breaker: error ratio threshold [0,1].")
	fs.IntVar(&f.breakerRequestVolume, "biz.login.breaker.request_volume", br.RequestVolume,
		"Login breaker: minimum request volume before evaluating.")
	fs.IntVar(&f.breakerSleepWindowSeconds, "biz.login.breaker.sleep_window_seconds", br.SleepWindowSeconds,
		"Login breaker: seconds to stay open before half-open.")
	fs.IntVar(&f.retryMaxAttempts, "biz.login.retry.max_attempts", rt.MaxAttempts,
		"Login DB retry: max attempts (including first).")
	fs.IntVar(&f.retryInitialBackoffMs, "biz.login.retry.initial_backoff_ms", rt.InitialBackoffMs,
		"Login DB retry: initial backoff in milliseconds.")
	fs.IntVar(&f.retryMaxBackoffMs, "biz.login.retry.max_backoff_ms", rt.MaxBackoffMs,
		"Login DB retry: max backoff in milliseconds.")
}

// buildLoginLimiter 从本域自持配置段构造登录限流器（令牌桶）。
// rate/burst 任一 <=0 即禁用态（nil，fail-open）；非法配置记日志后禁用。
func buildLoginLimiter(cfg LoginConfig) ratelimit.Limiter {
	rate := cfg.RateLimit.Rate
	burst := cfg.RateLimit.Burst
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

// buildLoginBreaker 从本域自持配置段构造登录 DB 查询熔断器。
//
// 配置键：
//
//	login:
//	  breaker:
//	    error_threshold: 0.5        # 错误率阈值（0-1），default 0.5
//	    request_volume: 20          # 最小请求量（低于此不评估），default 20
//	    sleep_window_seconds: 5     # Open 后多久转半开（秒），default 5
//
// 整段缺省时返回 nil（禁用态）——熔断是防御性增强，未配置则不介入。
// 用 sres 之外的 **hystrix**（阈值式）：sres 的概率式语义不符合契约对
// StateClosed 的定义（详见 交付报告与 e2e 测试注释）。
func buildLoginBreaker(cfg LoginConfig) circuitbreaker.CircuitBreaker {
	if !cfg.BreakerEnabled() {
		return nil // 未配置：禁用态
	}
	br := cfg.BreakerOrDefault()
	o := []hystrix.Option{}
	if br.ErrorThreshold > 0 {
		o = append(o, hystrix.WithErrorThreshold(br.ErrorThreshold))
	}
	if br.RequestVolume > 0 {
		o = append(o, hystrix.WithRequestVolumeThreshold(br.RequestVolume))
	}
	if br.SleepWindowSeconds > 0 {
		o = append(o, hystrix.WithSleepWindow(time.Duration(br.SleepWindowSeconds)*time.Second))
	}
	cb := hystrix.New(o...)
	baldlog.Info(context.Background(), "login breaker enabled")
	return cb
}

// buildLoginRetrier 从本域自持配置段构造登录 DB 查询重试器。
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
func buildLoginRetrier(cfg LoginConfig) *retry.Retrier {
	if !cfg.RetryEnabled() {
		return nil // 未配置：禁用态
	}
	rt := cfg.RetryOrDefault()
	o := []retry.Option{
		retry.WithClassifier(authbiz.RetryOnTransientDBError),
	}
	if rt.MaxAttempts >= 1 {
		o = append(o, retry.WithMaxAttempts(rt.MaxAttempts))
	}
	backoff := retry.ExponentialBackoff{
		Initial: 200 * time.Millisecond,
		Factor:  2,
		Max:     10 * time.Second,
	}
	if rt.InitialBackoffMs > 0 {
		backoff.Initial = time.Duration(rt.InitialBackoffMs) * time.Millisecond
	}
	if rt.MaxBackoffMs > 0 {
		backoff.Max = time.Duration(rt.MaxBackoffMs) * time.Millisecond
	}
	o = append(o, retry.WithBackoff(backoff), retry.WithJitter(retry.FullJitter))
	r := retry.New(o...)
	baldlog.Info(context.Background(), "login retrier enabled",
		"initial_backoff_ms", backoff.Initial.Milliseconds(), "max_backoff_ms", backoff.Max.Milliseconds())
	return r
}
