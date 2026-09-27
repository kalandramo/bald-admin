package options

import (
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
)

// W2 配置聚合：ServerOptions 的默认值、flag 绑定、校验、解码。
//
// 设计对齐 miniblog docs/08：配置项聚合到结构体 + NewServerOptions 默认值
// + AddFlags 绑定命令行 + Validate 聚合校验 + 由配置文件覆盖默认值。

func TestNewServerOptions_Defaults(t *testing.T) {
	o := NewServerOptions()

	// 默认值必须让「零配置」可启动（文档 08：默认值提升可用性）。
	if o.Login.RateLimit.Rate != 0 || o.Login.RateLimit.Burst != 0 {
		t.Errorf("rate limit should default to disabled (0), got %v/%v",
			o.Login.RateLimit.Rate, o.Login.RateLimit.Burst)
	}
	// 防御性能力默认关闭：Breaker/Retry 保持 nil（「段存在即启用」语义）。
	if o.BreakerEnabled() {
		t.Error("breaker should default to disabled (nil), got enabled")
	}
	if o.RetryEnabled() {
		t.Error("retry should default to disabled (nil), got enabled")
	}
	// 启用态的字段缺省值由 OrDefault 提供。
	if rt := o.RetryOrDefault(); rt.MaxAttempts != 3 {
		t.Errorf("retry.max_attempts default = %d, want 3", rt.MaxAttempts)
	}
	if br := o.BreakerOrDefault(); br.ErrorThreshold != 0.5 {
		t.Errorf("breaker.error_threshold default = %v, want 0.5", br.ErrorThreshold)
	}
	if o.Auth.AccessTTLMinutes != 120 {
		t.Errorf("auth.access_ttl_minutes default = %d, want 120", o.Auth.AccessTTLMinutes)
	}
	if o.Auth.RefreshTTLHours != 168 {
		t.Errorf("auth.refresh_ttl_hours default = %d, want 168", o.Auth.RefreshTTLHours)
	}
	if o.Audit.FallbackTenant != "t-default" {
		t.Errorf("audit.fallback_tenant default = %q, want t-default", o.Audit.FallbackTenant)
	}
	if o.Gateway.Addr != ":8081" {
		t.Errorf("gateway.addr default = %q, want :8081", o.Gateway.Addr)
	}
}

// 关键行为保持：未配置的段不因「有默认值」而被静默启用。
func TestNewServerOptions_DisabledSegmentsStayDisabled(t *testing.T) {
	o := NewServerOptions()
	if err := o.Validate(); err != nil {
		t.Fatalf("zero-config must be valid, got %v", err)
	}
	if o.BreakerEnabled() || o.RetryEnabled() {
		t.Fatal("absent segments must not be enabled by defaults")
	}
}

// 校验：非法值必须被拒绝（而非静默接受）。
func TestServerOptions_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ServerOptions)
		wantErr bool
	}{
		{"默认值合法", func(*ServerOptions) {}, false},
		{"限流 rate 为负", func(o *ServerOptions) { o.Login.RateLimit.Rate = -1 }, true},
		{"重试次数为负（启用后）", func(o *ServerOptions) { o.Login.Retry = &RetryOptions{MaxAttempts: -1} }, true},
		{"TTL 为零", func(o *ServerOptions) { o.Auth.AccessTTLMinutes = 0 }, true},
		{"gateway 地址为空", func(o *ServerOptions) { o.Gateway.Addr = "" }, true},
		{"熔断错误率越界（启用后）", func(o *ServerOptions) { o.Login.Breaker = &BreakerOptions{ErrorThreshold: 1.5} }, true},
		{"SSE addr 无 path", func(o *ServerOptions) { o.SSE.Addr = ":8090" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := NewServerOptions()
			tc.mutate(o)
			err := o.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// 校验必须**聚合**全部错误（文档 08 的 utilerrors 风格），不是 fail-fast 首个。
func TestServerOptions_ValidateAggregatesAllErrors(t *testing.T) {
	o := NewServerOptions()
	o.Login.RateLimit.Rate = -1
	o.Auth.AccessTTLMinutes = 0
	o.Gateway.Addr = ""

	err := o.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want aggregated error")
	}
	msg := err.Error()
	// 三处非法值都应出现在同一条错误里。
	for _, frag := range []string{"rate", "access_ttl", "gateway"} {
		if !strings.Contains(msg, frag) {
			t.Errorf("aggregated error %q missing %q", msg, frag)
		}
	}
}

// 关键回归：AddFlags 不得把「未配置的段」变成启用态。
//
// 实测抓到的真实缺陷（本会话自查发现并修复）：AddFlags 直接绑
// &o.Login.Breaker.X 会立刻 materialize 指针，「段缺省」变「段存在」→
// 熔断/重试被静默启用（与「未配置不介入」契约冲突）。
// 修复：绑到影子字段 flagShadow，指针仅在配置解码时按段存在性建立。
func TestAddFlags_DoesNotEnableAbsentSegments(t *testing.T) {
	o := NewServerOptions()
	if o.BreakerEnabled() || o.RetryEnabled() {
		t.Fatal("precondition: defense features start disabled")
	}

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	o.AddFlags(fs)

	if o.BreakerEnabled() {
		t.Error("AddFlags must not enable breaker (segment absent)")
	}
	if o.RetryEnabled() {
		t.Error("AddFlags must not enable retry (segment absent)")
	}

	// 解码不含这两段的配置后，仍须保持禁用。
	if err := mapstructure.Decode(map[string]any{
		"file": map[string]any{"bucket": "b"},
	}, o); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if o.BreakerEnabled() || o.RetryEnabled() {
		t.Error("absent segments must stay disabled after decode")
	}

	// 反向：配置存在该段时应启用（指针建立），且字段值被正确解码。
	if err := mapstructure.Decode(map[string]any{
		"login": map[string]any{
			"breaker": map[string]any{"error_threshold": 0.6},
			"retry":   map[string]any{"max_attempts": 5},
		},
	}, o); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !o.BreakerEnabled() {
		t.Error("breaker segment present but not enabled")
	}
	if !o.RetryEnabled() {
		t.Error("retry segment present but not enabled")
	}
	if br := o.BreakerOrDefault(); br.ErrorThreshold != 0.6 {
		t.Errorf("decoded breaker.error_threshold = %v, want 0.6", br.ErrorThreshold)
	}
	if rt := o.RetryOrDefault(); rt.MaxAttempts != 5 {
		t.Errorf("decoded retry.max_attempts = %d, want 5", rt.MaxAttempts)
	}
}
