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
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
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
	Metrics MetricsOptions `mapstructure:"metrics"`

	// flagShadow 是 AddFlags 的绑定目标（pflag 需要一个可写地址）。
	//
	// 为什么不直接绑到 Login.Breaker / Login.Retry：那两段用指针表达「段是否
	// 存在」，而 pflag 绑定会立刻 materialize 指针——「未配置」于是变成「启用」，
	// 静默打开熔断/重试（实测复现：AddFlags 后 BreakerEnabled=true）。
	// 绑定到影子字段后，指针仅在**配置快照解码**（Decode）时按段存在性建立；
	// flag 值仍经 flattenFlags（读 Value.String()，与绑定地址无关）进入配置合并。
	flagShadow flagShadow
}

// flagShadow 承载 AddFlags 的绑定地址（不参与 mapstructure 解码——无 tag）。
type flagShadow struct {
	breakerErrorThreshold     float64
	breakerRequestVolume      int
	breakerSleepWindowSeconds int
	retryMaxAttempts          int
	retryInitialBackoffMs     int
	retryMaxBackoffMs         int
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

// MetricsOptions 可观测性暴露端的历史 env 通道（**已退役**）。
//
// 退役理由（W3）：本结构的字段曾由 BALD_ADMIN_METRICS_ADDR /
// BALD_ADMIN_OTLP_ADDR 填充，经由 applyObservabilityDefaults 在**契约装载
// 之前**注入。但那两个短名会被框架 env 层截获成 `metrics.addr` /
// `metrics.otlp`（契约无此键，被 DiscardUnknown 丢弃）；第二次完整装载
// （syncBootstrap）又用配置文件的值覆盖了注入结果——净效果是「配置里声明了
// metrics 段时，env 静默失效」，且无任何提示。
//
// 现统一走框架路径名（`BALD_ADMIN_METRICS_PROMETHEUS_ADDR` /
// `BALD_ADMIN_METRICS_OTLP_ENDPOINT` / `BALD_ADMIN_TRACER_OTLP_ENDPOINT`），
// 优先级与其余契约字段一致（env 层高于本地文件层）。
//
// 字段与类型保留仅为兼容「程序化覆写」的既有结构（无 flag 绑定）；
// 运行期消费者已删，改端口/端点请用上述路径名或对应配置段。
type MetricsOptions struct {
	// Addr 历史字段（无消费者）。改暴露端口请用 `metrics.prometheus.addr`。
	Addr string `mapstructure:"addr"`
	// OtlpAddr 历史字段（无消费者）。改端点请用 `metrics.otlp.endpoint`
	// 与 `tracer.otlp.endpoint`（两者需分别设置）。
	OtlpAddr string `mapstructure:"otlp_addr"`
}

// DecodeInto 把配置快照（框架装载合并后的嵌套 map）解码进本配置对象。
//
// 语义（对齐 miniblog docs/08 的 viper.Unmarshal 步骤）：**用配置内容覆盖默认值**
// ——未被配置覆盖的字段保留 NewServerOptions() 填入的默认值。
// 指针段（Breaker/Retry）只在配置中**存在该段**时建立，故「段缺省即禁用」的
// 契约语义得以保持（AddFlags 不 materialize 指针，见 flagShadow 注释）。
//
// 调用时机：必须在使用配置之前（运行期装配钩子内），且仅一次。
func (o *ServerOptions) DecodeInto(settings map[string]any) error {
	if settings == nil {
		return nil
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           o,
		TagName:          "mapstructure",
		WeaklyTypedInput: true,  // yaml/flag 值多为 string/int，宽松转换到目标类型
		ErrorUnused:      false, // 框架契约键（server/log/...）不属于本结构体，忽略
	})
	if err != nil {
		return fmt.Errorf("server options: build decoder: %w", err)
	}
	if err := dec.Decode(settings); err != nil {
		return fmt.Errorf("server options: decode: %w", err)
	}
	return nil
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
		File:  FileOptions{Bucket: ""}, // 空 = 未配置对象存储（file 模块降级）
		Audit: AuditOptions{FallbackTenant: "t-default"},
		Auth:  AuthOptions{AccessTTLMinutes: 120, RefreshTTLHours: 168},
		// Asynq/Redis/SSE 的 env 兼容通道（构造期需要，见各字段注释）。
		// 集中在此处读取——替换前它们散落在 asynq.go/sse.go/servers.go 各自
		// os.Getenv（「同源配置多条链」），现收敛为唯一默认值来源。
		//
		// 注：Metrics/Gateway 的短名 env（BALD_ADMIN_METRICS_ADDR /
		// BALD_ADMIN_OTLP_ADDR / BALD_GATEWAY_ADDR）已退役——它们会被框架
		// env 层截获成契约不存在的键而被静默丢弃，且写入时机早于契约装载、
		// 会被配置段覆盖。改用框架路径名后优先级与其余字段一致。
		// SSE 例外：契约 `server.sse` 段无 provider（registered=false），
		// 用路径名会 fail-fast，故保留短名通道（见 sse.go 文件头）。
		Asynq: AsynqOptions{Codec: envDefault("BALD_ADMIN_ASYNQ_CODEC", "json")},
		Redis: RedisOptions{Addr: os.Getenv("BALD_ADMIN_REDIS_ADDR")},
		SSE:   SSEOptions{Addr: os.Getenv("BALD_ADMIN_SSE_ADDR"), Path: os.Getenv("BALD_ADMIN_SSE_PATH")},
		// gateway 缺省 :8081（与迁移前一致；Validate 要求非空）。此值仅在
		// 契约 `server.gateway` 段缺失时被合成使用——若配置段或
		// `BALD_ADMIN_SERVER_GATEWAY_ADDR` 提供了值，显式值优先。
		Gateway: GatewayOptions{Addr: ":8081"},
		Metrics: MetricsOptions{},
	}
}

// envDefault 取环境变量默认值：未设置时回退 def。
func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

	// 熔断 / 重试：绑到影子字段（**不** materialize Login.Breaker/Retry 指针）。
	//
	// 必要性（实测复现的缺陷）：直接绑 &o.Login.Breaker.X 会立即建立指针 →
	// 「段缺省」变成「段存在」→ 静默启用熔断/重试，与「未配置不介入」契约冲突。
	// flag 值本身经 flattenFlags 读 Value.String() 进入配置合并（与绑定地址
	// 无关），故影子字段不影响 flag 的优先级链；解码时若配置含该段，指针自然建立。
	br := breakerDefaults()
	rt := retryDefaults()
	fs.Float64Var(&o.flagShadow.breakerErrorThreshold, "login.breaker.error_threshold", br.ErrorThreshold,
		"Login breaker: error ratio threshold [0,1].")
	fs.IntVar(&o.flagShadow.breakerRequestVolume, "login.breaker.request_volume", br.RequestVolume,
		"Login breaker: minimum request volume before evaluating.")
	fs.IntVar(&o.flagShadow.breakerSleepWindowSeconds, "login.breaker.sleep_window_seconds", br.SleepWindowSeconds,
		"Login breaker: seconds to stay open before half-open.")
	fs.IntVar(&o.flagShadow.retryMaxAttempts, "login.retry.max_attempts", rt.MaxAttempts,
		"Login DB retry: max attempts (including first).")
	fs.IntVar(&o.flagShadow.retryInitialBackoffMs, "login.retry.initial_backoff_ms", rt.InitialBackoffMs,
		"Login DB retry: initial backoff in milliseconds.")
	fs.IntVar(&o.flagShadow.retryMaxBackoffMs, "login.retry.max_backoff_ms", rt.MaxBackoffMs,
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
