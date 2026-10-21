package authd

// config_test.go —— 登录防护配置与构造器的域内单测。
//
// 覆盖 login 段的默认值/段存在性/校验/flag 影子字段语义，载体为 LoginConfig。

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// 段缺失合法：全字段零值 = 三件全禁用（「未配置不介入」契约）。
func TestDecodeLogin_AbsentSectionStaysDisabled(t *testing.T) {
	cfg, err := decodeLogin(map[string]any{
		"biz": map[string]any{"audit": map[string]any{"fallback_tenant": "t-default"}},
	})
	if err != nil {
		t.Fatalf("decodeLogin: %v", err)
	}
	if cfg.RateLimit.Rate != 0 || cfg.RateLimit.Burst != 0 {
		t.Errorf("rate limit should decode to disabled (0), got %v/%v", cfg.RateLimit.Rate, cfg.RateLimit.Burst)
	}
	if cfg.BreakerEnabled() || cfg.retryEnabled() {
		t.Fatal("absent segments must not be enabled")
	}
	if buildLoginLimiter(cfg) != nil {
		t.Error("limiter should be nil (disabled)")
	}
	if buildLoginBreaker(cfg) != nil {
		t.Error("breaker should be nil (disabled)")
	}
	if buildLoginRetrier(cfg) != nil {
		t.Error("retrier should be nil (disabled)")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero-config login must be valid, got %v", err)
	}
}

// 段存在即启用：指针按配置内容建立，字段值正确解码（原 TestAddFlags 反向用例）。
func TestDecodeLogin_PresenceEnables(t *testing.T) {
	cfg, err := decodeLogin(map[string]any{
		"biz": map[string]any{"login": map[string]any{
			"breaker": map[string]any{"error_threshold": 0.6, "request_volume": 20, "sleep_window_seconds": 5},
			"retry":   map[string]any{"max_attempts": 5},
		}},
	})
	if err != nil {
		t.Fatalf("decodeLogin: %v", err)
	}
	if !cfg.BreakerEnabled() {
		t.Error("breaker segment present but not enabled")
	}
	if !cfg.retryEnabled() {
		t.Error("retry segment present but not enabled")
	}
	if br := cfg.breakerOrDefault(); br.ErrorThreshold != 0.6 {
		t.Errorf("decoded breaker.error_threshold = %v, want 0.6", br.ErrorThreshold)
	}
	if rt := cfg.retryOrDefault(); rt.MaxAttempts != 5 {
		t.Errorf("decoded retry.max_attempts = %d, want 5", rt.MaxAttempts)
	}
	if buildLoginBreaker(cfg) == nil {
		t.Error("breaker should be constructed when enabled")
	}
	if buildLoginRetrier(cfg) == nil {
		t.Error("retrier should be constructed when enabled")
	}
}

// flag 层的值以字符串进配置合并（flattenFlags 取 Value.String()）——弱类型解码
// 必须吃下 string→float/int（与迁前 ServerOptions.DecodeInto 同一解码配置）。
func TestDecodeLogin_WeaklyTypedFlagValues(t *testing.T) {
	cfg, err := decodeLogin(map[string]any{
		"biz": map[string]any{"login": map[string]any{
			"rate_limit": map[string]any{"rate": "2", "burst": "4"},
		}},
	})
	if err != nil {
		t.Fatalf("decodeLogin: %v", err)
	}
	if cfg.RateLimit.Rate != 2 || cfg.RateLimit.Burst != 4 {
		t.Errorf("weak decode = %v/%v, want 2/4", cfg.RateLimit.Rate, cfg.RateLimit.Burst)
	}
	if buildLoginLimiter(cfg) == nil {
		t.Error("limiter should be constructed for rate=2 burst=4")
	}
}

// 段内未知键必须报错（严格解码：业务键笔误启动期暴露，而非静默落默认值）。
func TestDecodeLogin_StrictUnknownKey(t *testing.T) {
	_, err := decodeLogin(map[string]any{
		"biz": map[string]any{"login": map[string]any{
			"ratelimit": map[string]any{"rate": 1}, // 笔误：应为 rate_limit
		}},
	})
	if err == nil {
		t.Fatal("decodeLogin = nil error, want unknown-key error")
	}
}

// 段型错误必须报错（biz.login 非映射）。
func TestDecodeLogin_SectionMustBeMap(t *testing.T) {
	_, err := decodeLogin(map[string]any{
		"biz": map[string]any{"login": 5},
	})
	if err == nil || !strings.Contains(err.Error(), "must be a map") {
		t.Fatalf("decodeLogin err = %v, want map-type error", err)
	}
}

// 校验：非法值必须被拒绝（原 options.Validate 表的 login 行）。
func TestLoginConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     LoginConfig
		wantErr bool
	}{
		{"零值合法", LoginConfig{}, false},
		{"限流 rate 为负", LoginConfig{RateLimit: rateLimitConfig{Rate: -1}}, true},
		{"限流 burst 为负", LoginConfig{RateLimit: rateLimitConfig{Burst: -1}}, true},
		{"重试次数为负（启用后）", LoginConfig{Retry: &retryConfig{MaxAttempts: -1}}, true},
		{"熔断错误率越界（启用后）", LoginConfig{Breaker: &breakerConfig{ErrorThreshold: 1.5}}, true},
		{"熔断请求量为零（启用后）", LoginConfig{Breaker: &breakerConfig{ErrorThreshold: 0.5}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// 校验必须**聚合**全部错误，不是 fail-fast 首个。
func TestLoginConfig_ValidateAggregatesAllErrors(t *testing.T) {
	cfg := LoginConfig{
		RateLimit: rateLimitConfig{Rate: -1},
		Retry:     &retryConfig{MaxAttempts: 0},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want aggregated error")
	}
	msg := err.Error()
	for _, frag := range []string{"rate_limit.rate", "retry.max_attempts"} {
		if !strings.Contains(msg, frag) {
			t.Errorf("aggregated error %q missing %q", msg, frag)
		}
	}
}

// AddFlags 注册全部 biz.login.* 键且默认值正确（-h 可见性）；影子字段保证
// 绑定动作本身不改变任何配置状态（原影子纪律测试的域内形态）。
func TestFlagOptions_AddFlags(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	var f FlagOptions
	f.AddFlags(fs)

	wantDefaults := map[string]string{
		"biz.login.rate_limit.rate":                   "0",
		"biz.login.rate_limit.burst":                  "0",
		"biz.login.breaker.error_threshold":           "0.5",
		"biz.login.breaker.request_volume":            "20",
		"biz.login.breaker.sleep_window_seconds":      "5",
		"biz.login.retry.max_attempts":                "3",
		"biz.login.retry.initial_backoff_ms":          "200",
		"biz.login.retry.max_backoff_ms":              "10000",
	}
	for name, def := range wantDefaults {
		fl := fs.Lookup(name)
		if fl == nil {
			t.Errorf("flag %q not registered", name)
			continue
		}
		if fl.DefValue != def {
			t.Errorf("flag %q default = %q, want %q", name, fl.DefValue, def)
		}
	}
	registered := 0
	fs.VisitAll(func(*pflag.Flag) { registered++ })
	if registered != len(wantDefaults) {
		t.Errorf("registered %d flags, want %d", registered, len(wantDefaults))
	}
}
