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
// 限流键误写（rate_limit 误为 ratelimit）时限流静默禁用且无任何
// 报错。强类型结构体使拼错在编译期/启动期暴露。
//
// 命名约束（框架语义）：本包字段的 mapstructure tag 均为点路径的最后一段，前缀由
// 嵌套结构体表达；业务段统一挂在 BusinessSection（biz）之下，与框架契约段在命名
// 空间上分离。
//
// 下划线字段（fallback_tenant / access_ttl_minutes 等）可正常经 env 表达——env 名
// 的下划线被切段后，由 bconf.lookupField 的折叠匹配折回契约字段名
// （见 bootstrap/config/merge.go:environMap 与 bconf/unmarshal.go:foldPath）。
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
//	login 段（登录防护三件套）住 auth 域声明包
//	（internal/apiserver/domains/authd 的 config.go）——键名/默认值/校验/构造
//	均归域自持，本结构不再出现业务阈值；flag 通道经 authd.FlagOptions 声明。
//	file:    {bucket: go-bald-admin}
//	audit:   {fallback_tenant: t-default}
//	auth:    {access_ttl_minutes: 120}
//	asynq:   {codec: json}
//	redis:   {addr: "127.0.0.1:6379"}
//	sse:     {addr: ":8090", path: "/events"}
//	gateway: {addr: ":8081"}
type ServerOptions struct {
	File    fileOptions    `mapstructure:"file"`
	Audit   auditOptions   `mapstructure:"audit"`
	Auth    authOptions    `mapstructure:"auth"`
	Asynq   asynqOptions   `mapstructure:"asynq"`
	Redis   redisOptions   `mapstructure:"redis"`
	SSE     sseOptions     `mapstructure:"sse"`

	// flagShadow 是 AddFlags 的绑定目标（pflag 需要一个可写地址）。
	//
	// 为什么不直接绑到 Login.Breaker / Login.Retry：那两段用指针表达「段是否
	// 存在」，而 pflag 绑定会立刻 materialize 指针——「未配置」于是变成「启用」，
	// 静默打开熔断/重试（实测复现：AddFlags 后 BreakerEnabled=true）。
	// login 段的 flag 绑定（含影子字段纪律）住 authd.FlagOptions。
}

// fileOptions 文件模块（对象存储桶；bconf storage 段无 bucket 字段）。
type fileOptions struct {
	Bucket string `mapstructure:"bucket"`
}

// auditOptions 审计（兜底租户：login 失败/permission 等无租户事件落库归属）。
type auditOptions struct {
	FallbackTenant string `mapstructure:"fallback_tenant"`
}

// authOptions 令牌生命周期。
type authOptions struct {
	AccessTTLMinutes int `mapstructure:"access_ttl_minutes"`
	RefreshTTLHours  int `mapstructure:"refresh_ttl_hours"`
}

// asynqOptions 任务队列。
type asynqOptions struct {
	Codec string `mapstructure:"codec"`
}

// redisOptions 业务侧 Redis 地址（任务队列/缓存复用的同一实例）。
// 与 bconf cache.redis 段并存：本字段是 env 兼容通道（wire 构造期需要）。
type redisOptions struct {
	Addr string `mapstructure:"addr"`
}

// sseOptions SSE 传输轴（空 addr = 不启用）。
type sseOptions struct {
	Addr string `mapstructure:"addr"`
	Path string `mapstructure:"path"`
}

// BusinessSection 是 bald-admin 业务配置的**顶层段名**。
//
// 业务配置统一收纳于此后，与框架契约段（server/audit/metrics/…）在命名空间上
// 彻底分离——这是本包存在的理由：此前业务键寄生在契约段名下（如
// `audit.fallback_tenant`），框架无法区分「用户笔误」与「业务自持键」，导致契约
// 段无法做未知键校验（两者在框架视角都是「契约段内的未知键」）。
//
// 命名选择：契约永不会声明 biz（框架级契约不含「业务」概念），天然无冲突。
// 该常量由本包自持——**框架不需要知道它**：框架侧的严格校验白名单由应用经
// bootstrap/config.Options.AllowBusinessSections 传入，框架零业务概念。
const BusinessSection = "biz"

// DecodeInto 把配置快照（框架装载合并后的嵌套 map）解码进本配置对象。
//
// 语义（对齐 miniblog docs/08 的 viper.Unmarshal 步骤）：**用配置内容覆盖默认值**
// ——未被配置覆盖的字段保留 NewServerOptions() 填入的默认值。
// 指针段（Breaker/Retry）只在配置中**存在该段**时建立，故「段缺省即禁用」的
// 契约语义得以保持（AddFlags 不 materialize 指针，见 flagShadow 注释）。
//
// **只解 biz 子树**（见 BusinessSection）：业务键全部位于 biz 之下，故不再需要
// 与契约段共享命名空间，ErrorUnused 可安全开启——业务键的笔误（如
// `falback_tenant`）从此**启动期报错**而非静默落默认值。这是本包头部注释所述
// 「拼错静默取零值」缺陷在业务侧的收口（框架侧同类能力见 bconf.FindUnknownKeys）。
//
// 调用时机：必须在使用配置之前（运行期装配钩子内），且仅一次。
//
// ownedSections 列出**域自持段**（如 authd.ConfigSection）：这些子树不经本
// 解码（其键不映射到本结构任何字段，ErrorUnused 会误报），由拥有它的域包自行
// 严格解码与校验（authd.decodeLogin）。本结构只解自己消费的段。
func (o *ServerOptions) DecodeInto(settings map[string]any, ownedSections ...string) error {
	if settings == nil {
		return nil
	}
	// biz 段缺失（未配置任何业务项）是合法的——全字段保持默认值。
	var biz map[string]any
	if raw, ok := settings[BusinessSection]; ok && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("server options: %q must be a map, got %T", BusinessSection, raw)
		}
		if len(ownedSections) > 0 {
			owned := make(map[string]bool, len(ownedSections))
			for _, s := range ownedSections {
				owned[s] = true
			}
			filtered := make(map[string]any, len(m))
			for k, v := range m {
				if !owned[k] {
					filtered[k] = v
				}
			}
			m = filtered
		}
		biz = m
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           o,
		TagName:          "mapstructure",
		WeaklyTypedInput: true, // yaml/flag 值多为 string/int，宽松转换到目标类型
		// 只解 biz 子树后，本层已无框架契约键混入——未知键即业务键笔误，报错。
		ErrorUnused: true,
	})
	if err != nil {
		return fmt.Errorf("server options: build decoder: %w", err)
	}
	if err := dec.Decode(biz); err != nil {
		return fmt.Errorf("server options: decode %s section: %w", BusinessSection, err)
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
		File:  fileOptions{Bucket: ""}, // 空 = 未配置对象存储（file 模块降级）
		Audit: auditOptions{FallbackTenant: "t-default"},
		Auth:  authOptions{AccessTTLMinutes: 120, RefreshTTLHours: 168},
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
		Asynq:   asynqOptions{Codec: envDefault("BALD_ADMIN_ASYNQ_CODEC", "json")},
		Redis:   redisOptions{Addr: os.Getenv("BALD_ADMIN_REDIS_ADDR")},
		SSE:     sseOptions{Addr: os.Getenv("BALD_ADMIN_SSE_ADDR"), Path: os.Getenv("BALD_ADMIN_SSE_PATH")},
	}
}

// envDefault 取环境变量默认值：未设置时回退 def。
func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// AddFlags 把配置项绑定到命令行选项（pflag）。
//
// 收益（文档 08「通过命令行选项设置配置项」）：`bald-admin -h` 即可见全部业务
// 配置键、说明与默认值；小配置场景无需维护配置文件。
//
// 键名用点路径（与配置文件键、mapstructure tag 三者对齐）。
// （login 段的 --biz.login.* 八个旗标住 authd.FlagOptions——
// 键名/默认值/影子字段纪律归域自持，装配层仅 Bind 声明。）
func (o *ServerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.File.Bucket, "biz.file.bucket", o.File.Bucket,
		"Object storage bucket used by the file module.")
	fs.StringVar(&o.Audit.FallbackTenant, "biz.audit.fallback_tenant", o.Audit.FallbackTenant,
		"Tenant assigned to audit events that carry no tenant.")
	fs.IntVar(&o.Auth.AccessTTLMinutes, "biz.auth.access_ttl_minutes", o.Auth.AccessTTLMinutes,
		"Access token TTL in minutes.")
	fs.IntVar(&o.Auth.RefreshTTLHours, "biz.auth.refresh_ttl_hours", o.Auth.RefreshTTLHours,
		"Refresh token TTL in hours.")
	fs.StringVar(&o.Asynq.Codec, "biz.asynq.codec", o.Asynq.Codec,
		"Asynq task payload codec.")
	fs.StringVar(&o.Redis.Addr, "biz.redis.addr", o.Redis.Addr,
		"Redis address shared by task queue and cache.")
	fs.StringVar(&o.SSE.Addr, "biz.sse.addr", o.SSE.Addr,
		"SSE transport listen address (empty disables).")
	fs.StringVar(&o.SSE.Path, "biz.sse.path", o.SSE.Path,
		"SSE transport route path.")
}

// Validate 校验配置合法性，**聚合**全部错误后一次性返回。
//
// 为什么聚合而非 fail-fast 首个：配置问题常成组出现（如整段复制后忘记改），
// 一次报全可免去「改一个跑一次」的往返（文档 08 的 utilerrors.NewAggregate 风格）。
func (o *ServerOptions) Validate() error {
	var errs []string

	// （login 段的校验住 authd（LoginConfig.Validate）——聚合错误
	// 风格不变，域 Wire 解码时 fail-fast。）

	// 令牌 TTL：必须为正（零值会让签发的 token 立即过期）。
	if o.Auth.AccessTTLMinutes <= 0 {
		errs = append(errs, fmt.Sprintf("auth.access_ttl_minutes must be > 0 (got %d)", o.Auth.AccessTTLMinutes))
	}
	if o.Auth.RefreshTTLHours <= 0 {
		errs = append(errs, fmt.Sprintf("auth.refresh_ttl_hours must be > 0 (got %d)", o.Auth.RefreshTTLHours))
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
