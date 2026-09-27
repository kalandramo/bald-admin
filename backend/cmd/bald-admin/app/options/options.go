// Package options 聚合 bald-admin 的**全部业务初始化配置项**。
//
// 设计对齐 miniblog docs/08《应用构建（2）：如何给应用添加配置功能？》：
//   - 配置项聚合到 ServerOptions 结构体（而非散落的字符串点路径读取）；
//   - NewServerOptions() 提供默认值（零配置可启动，文档 08「创建默认的配置项」）；
//   - AddFlags() 绑定命令行选项（「零成本获知配置项」：-h 即见全部键与默认值）；
//   - Validate() 聚合校验（文档 08 的 utilerrors 风格：收集全部错误而非首个）。
//
// 为什么需要（替换前的缺陷）：业务配置以字符串点路径散读（configFloat /
// GetString / os.Getenv 共 13 处），配置键拼错时**静默取零值**——例如
// `login.rate_limit.rate` 误写为 `login.ratelimit.rate`，限流静默禁用且无任何
// 报错。强类型结构体使拼错在编译期/启动期暴露。
//
// 命名约束（框架语义）：环境变量按下划线切段映射点路径，故业务键**避免下划线**，
// 用点路径或 yaml/flag 覆盖（见 bootstrap/config/merge.go:environMap 注释）。
// 本包字段的 mapstructure tag 均为点路径的最后一段，前缀由嵌套结构体表达。
package options

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// ServerOptions 是 bald-admin 的业务初始化配置（全部业务自持段）。
//
// 嵌套结构与配置文件同构，故 mapstructure 可递归解码：
//
//	login:
//	  rate_limit: {rate: 1, burst: 5}
//	  breaker:    {error_threshold: 0.5}
//	  retry:      {max_attempts: 3}
//	file:    {bucket: go-bald-admin}
//	audit:   {fallback_tenant: t-default}
//	auth:    {access_ttl_minutes: 120}
//	asynq:   {codec: json}
//	redis:   {addr: "127.0.0.1:6379"}
//	sse:     {addr: ":8090", path: "/events"}
//	gateway: {addr: ":8081"}
type ServerOptions struct {
	Login   LoginOptions   `mapstructure:"login"`
	File    FileOptions    `mapstructure:"file"`
	Audit   AuditOptions   `mapstructure:"audit"`
	Auth    AuthOptions    `mapstructure:"auth"`
	Asynq   AsynqOptions   `mapstructure:"asynq"`
	Redis   RedisOptions   `mapstructure:"redis"`
	SSE     SSEOptions     `mapstructure:"sse"`
	Gateway GatewayOptions `mapstructure:"gateway"`
}

// LoginOptions 登录防护（限流 / 熔断 / 重试三件套）。
//
// Breaker / Retry 用**指针**表达「整段配置是否存在」：契约语义是「段存在即启用、
// 缺省即禁用」（防御性增强，未配置不介入）。指针在 mapstructure 解码时对缺失的
// 段保持 nil（实测确认），故能精确保留该语义；值结构体会被默认值填充，
// 导致「未配置也启用」的行为回退。
type LoginOptions struct {
	RateLimit RateLimitOptions `mapstructure:"rate_limit"`
	// Breaker 非 nil = 启用登录 DB 查询熔断（段存在即启用）。
	Breaker *BreakerOptions `mapstructure:"breaker"`
	// Retry 非 nil = 启用登录 DB 查询重试（段存在即启用）。
	Retry *RetryOptions `mapstructure:"retry"`
}

// RateLimitOptions 登录限流（令牌桶）。零值=禁用（fail-open，不阻断登录）。
type RateLimitOptions struct {
	Rate  float64 `mapstructure:"rate"`
	Burst float64 `mapstructure:"burst"`
}

// BreakerOptions 登录 DB 查询熔断（阈值式 hystrix）。
type BreakerOptions struct {
	ErrorThreshold     float64 `mapstructure:"error_threshold"`
	RequestVolume      int     `mapstructure:"request_volume"`
	SleepWindowSeconds int     `mapstructure:"sleep_window_seconds"`
}

// RetryOptions 登录 DB 查询重试（瞬时故障）。
type RetryOptions struct {
	MaxAttempts      int `mapstructure:"max_attempts"`
	InitialBackoffMs int `mapstructure:"initial_backoff_ms"`
	MaxBackoffMs     int `mapstructure:"max_backoff_ms"`
}

// FileOptions 文件模块（对象存储桶；bconf storage 段无 bucket 字段）。
type FileOptions struct {
	Bucket string `mapstructure:"bucket"`
}

// AuditOptions 审计（兜底租户：login 失败/permission 等无租户事件落库归属）。
type AuditOptions struct {
	FallbackTenant string `mapstructure:"fallback_tenant"`
}

// AuthOptions 令牌生命周期。
type AuthOptions struct {
	AccessTTLMinutes int `mapstructure:"access_ttl_minutes"`
	RefreshTTLHours  int `mapstructure:"refresh_ttl_hours"`
}

// AsynqOptions 任务队列。
type AsynqOptions struct {
	Codec string `mapstructure:"codec"`
}

// RedisOptions 业务侧 Redis 地址（任务队列/缓存复用的同一实例）。
// 与 bconf cache.redis 段并存：本字段是 env 兼容通道（wire 构造期需要）。
type RedisOptions struct {
	Addr string `mapstructure:"addr"`
}

// SSEOptions SSE 传输轴（空 addr = 不启用）。
type SSEOptions struct {
	Addr string `mapstructure:"addr"`
	Path string `mapstructure:"path"`
}

// GatewayOptions grpc-gateway 转码面监听地址。
type GatewayOptions struct {
	Addr string `mapstructure:"addr"`
}

// NewServerOptions 创建带默认值的配置。
//
// 默认值原则（文档 08）：合理的初始设置使程序在无配置/配置缺失时仍能运行。
// 防御性能力（限流/熔断/重试）默认**关闭**——Breaker/Retry 保持 nil（段缺省即
// 禁用），与替换前的「cfg.Get(段) 不存在即返回 nil」语义逐字一致（行为不回退）。
func NewServerOptions() *ServerOptions {
	return &ServerOptions{
		Login: LoginOptions{
			// RateLimit 零值 = 禁用（rate<=0 即不介入，见 buildLoginLimiter）。
			RateLimit: RateLimitOptions{Rate: 0, Burst: 0},
			// Breaker / Retry 保持 nil = 禁用。启用时由配置段提供值，
			// 各字段的缺省由 breakerDefaults/retryDefaults 兜底。
			Breaker: nil,
			Retry:   nil,
		},
		File:    FileOptions{Bucket: ""}, // 空 = 未配置对象存储（file 模块降级）
		Audit:   AuditOptions{FallbackTenant: "t-default"},
		Auth:    AuthOptions{AccessTTLMinutes: 120, RefreshTTLHours: 168},
		Asynq:   AsynqOptions{Codec: "json"},
		Redis:   RedisOptions{Addr: ""},
		SSE:     SSEOptions{Addr: "", Path: ""},
		Gateway: GatewayOptions{Addr: ":8081"},
	}
}

// breakerDefaults / retryDefaults 返回启用态下的字段缺省值（段存在但字段未写时）。
// 与替换前 buildLoginBreaker/buildLoginRetrier 的「>0 才覆盖」语义一致。
func breakerDefaults() BreakerOptions {
	return BreakerOptions{ErrorThreshold: 0.5, RequestVolume: 20, SleepWindowSeconds: 5}
}

func retryDefaults() RetryOptions {
	return RetryOptions{MaxAttempts: 3, InitialBackoffMs: 200, MaxBackoffMs: 10000}
}

// BreakerOrDefault 返回熔断配置：未配置（nil）时返回缺省值。
// 调用方需先判 Enabled（nil 检查）决定是否构造熔断器。
func (o *ServerOptions) BreakerOrDefault() BreakerOptions {
	if o.Login.Breaker == nil {
		return breakerDefaults()
	}
	return *o.Login.Breaker
}

// RetryOrDefault 返回重试配置：未配置（nil）时返回缺省值。
func (o *ServerOptions) RetryOrDefault() RetryOptions {
	if o.Login.Retry == nil {
		return retryDefaults()
	}
	return *o.Login.Retry
}

// BreakerEnabled / RetryEnabled 报告该能力是否被配置启用（段是否存在）。
func (o *ServerOptions) BreakerEnabled() bool { return o.Login.Breaker != nil }
func (o *ServerOptions) RetryEnabled() bool   { return o.Login.Retry != nil }

// AddFlags 把配置项绑定到命令行选项（pflag）。
//
// 收益（文档 08「通过命令行选项设置配置项」）：`bald-admin -h` 即可见全部业务
// 配置键、说明与默认值；小配置场景无需维护配置文件。
//
// 键名用点路径（与配置文件键、mapstructure tag 三者对齐）。
func (o *ServerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.Float64Var(&o.Login.RateLimit.Rate, "login.rate_limit.rate", o.Login.RateLimit.Rate,
		"Login rate limit: tokens per second (<=0 disables).")
	fs.Float64Var(&o.Login.RateLimit.Burst, "login.rate_limit.burst", o.Login.RateLimit.Burst,
		"Login rate limit: burst capacity (<=0 disables).")

	// 熔断 / 重试：用 flag **显式传入**即视为「配置该段」→ 启用。
	// 故先确保子结构体非 nil（pflag 需要写入地址），再绑定。
	// 未传 flag 且文件无该段时保持 nil（禁用态，语义与替换前一致）。
	if o.Login.Breaker == nil {
		b := breakerDefaults()
		o.Login.Breaker = &b
	}
	fs.Float64Var(&o.Login.Breaker.ErrorThreshold, "login.breaker.error_threshold", o.Login.Breaker.ErrorThreshold,
		"Login breaker: error ratio threshold [0,1].")
	fs.IntVar(&o.Login.Breaker.RequestVolume, "login.breaker.request_volume", o.Login.Breaker.RequestVolume,
		"Login breaker: minimum request volume before evaluating.")
	fs.IntVar(&o.Login.Breaker.SleepWindowSeconds, "login.breaker.sleep_window_seconds", o.Login.Breaker.SleepWindowSeconds,
		"Login breaker: seconds to stay open before half-open.")

	if o.Login.Retry == nil {
		r := retryDefaults()
		o.Login.Retry = &r
	}
	fs.IntVar(&o.Login.Retry.MaxAttempts, "login.retry.max_attempts", o.Login.Retry.MaxAttempts,
		"Login DB retry: max attempts (including first).")
	fs.IntVar(&o.Login.Retry.InitialBackoffMs, "login.retry.initial_backoff_ms", o.Login.Retry.InitialBackoffMs,
		"Login DB retry: initial backoff in milliseconds.")
	fs.IntVar(&o.Login.Retry.MaxBackoffMs, "login.retry.max_backoff_ms", o.Login.Retry.MaxBackoffMs,
		"Login DB retry: max backoff in milliseconds.")

	fs.StringVar(&o.File.Bucket, "file.bucket", o.File.Bucket,
		"Object storage bucket used by the file module.")
	fs.StringVar(&o.Audit.FallbackTenant, "audit.fallback_tenant", o.Audit.FallbackTenant,
		"Tenant assigned to audit events that carry no tenant.")
	fs.IntVar(&o.Auth.AccessTTLMinutes, "auth.access_ttl_minutes", o.Auth.AccessTTLMinutes,
		"Access token TTL in minutes.")
	fs.IntVar(&o.Auth.RefreshTTLHours, "auth.refresh_ttl_hours", o.Auth.RefreshTTLHours,
		"Refresh token TTL in hours.")
	fs.StringVar(&o.Asynq.Codec, "asynq.codec", o.Asynq.Codec,
		"Asynq task payload codec.")
	fs.StringVar(&o.Redis.Addr, "redis.addr", o.Redis.Addr,
		"Redis address shared by task queue and cache.")
	fs.StringVar(&o.SSE.Addr, "sse.addr", o.SSE.Addr,
		"SSE transport listen address (empty disables).")
	fs.StringVar(&o.SSE.Path, "sse.path", o.SSE.Path,
		"SSE transport route path.")
	fs.StringVar(&o.Gateway.Addr, "gateway.addr", o.Gateway.Addr,
		"grpc-gateway (REST transcoding) listen address.")
}

// Validate 校验配置合法性，**聚合**全部错误后一次性返回。
//
// 为什么聚合而非 fail-fast 首个：配置问题常成组出现（如整段复制后忘记改），
// 一次报全可免去「改一个跑一次」的往返（文档 08 的 utilerrors.NewAggregate 风格）。
func (o *ServerOptions) Validate() error {
	var errs []string

	// 限流：0=禁用合法；负值非法（无意义且掩盖配置笔误）。
	if o.Login.RateLimit.Rate < 0 {
		errs = append(errs, fmt.Sprintf("login.rate_limit.rate must be >= 0 (got %v)", o.Login.RateLimit.Rate))
	}
	if o.Login.RateLimit.Burst < 0 {
		errs = append(errs, fmt.Sprintf("login.rate_limit.burst must be >= 0 (got %v)", o.Login.RateLimit.Burst))
	}
	// 熔断：仅在启用（段存在）时校验；未配置即禁用，不产生错误。
	// 用 OrDefault 取值——nil 时回退缺省值，故禁用态天然合法。
	if br := o.BreakerOrDefault(); br.ErrorThreshold < 0 || br.ErrorThreshold > 1 {
		errs = append(errs, fmt.Sprintf("login.breaker.error_threshold must be within [0,1] (got %v)", br.ErrorThreshold))
	} else if o.BreakerEnabled() {
		if br.RequestVolume <= 0 {
			errs = append(errs, fmt.Sprintf("login.breaker.request_volume must be > 0 (got %d)", br.RequestVolume))
		}
		if br.SleepWindowSeconds <= 0 {
			errs = append(errs, fmt.Sprintf("login.breaker.sleep_window_seconds must be > 0 (got %d)", br.SleepWindowSeconds))
		}
	}
	// 重试：次数至少 1（含首次）；仅在启用时校验。
	if o.RetryEnabled() {
		if rt := o.RetryOrDefault(); rt.MaxAttempts < 1 {
			errs = append(errs, fmt.Sprintf("login.retry.max_attempts must be >= 1 (got %d)", rt.MaxAttempts))
		}
	}
	// 令牌 TTL：必须为正（零值会让签发的 token 立即过期）。
	if o.Auth.AccessTTLMinutes <= 0 {
		errs = append(errs, fmt.Sprintf("auth.access_ttl_minutes must be > 0 (got %d)", o.Auth.AccessTTLMinutes))
	}
	if o.Auth.RefreshTTLHours <= 0 {
		errs = append(errs, fmt.Sprintf("auth.refresh_ttl_hours must be > 0 (got %d)", o.Auth.RefreshTTLHours))
	}
	// gateway：监听地址不能为空（否则转码面静默消失）。
	if strings.TrimSpace(o.Gateway.Addr) == "" {
		errs = append(errs, "gateway.addr must not be empty")
	}
	// SSE：addr 与 path 要么都空（不启用），要么都非空。
	if (strings.TrimSpace(o.SSE.Addr) == "") != (strings.TrimSpace(o.SSE.Path) == "") {
		errs = append(errs, "sse.addr and sse.path must be set together (or both empty to disable)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid server options: %s", strings.Join(errs, "; "))
	}
	return nil
}

var _ = time.Second
