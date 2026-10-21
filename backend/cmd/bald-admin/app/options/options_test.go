package options

import (
	"strings"
	"testing"
)

// （login 段的默认值/段存在性/flag 影子/校验测试住
// internal/apiserver/domains/authd/config_test.go。）

// 配置聚合：ServerOptions 的默认值、flag 绑定、校验、解码。
//
// 设计对齐 miniblog docs/08：配置项聚合到结构体 + NewServerOptions 默认值
// + AddFlags 绑定命令行 + Validate 聚合校验 + 由配置文件覆盖默认值。

func TestNewServerOptions_Defaults(t *testing.T) {
	o := NewServerOptions()

	// 默认值必须让「零配置」可启动（文档 08：默认值提升可用性）。
	// （login 段默认值断言随域化迁往 authd/config_test.go。）
	if o.Auth.AccessTTLMinutes != 120 {
		t.Errorf("auth.access_ttl_minutes default = %d, want 120", o.Auth.AccessTTLMinutes)
	}
	if o.Auth.RefreshTTLHours != 168 {
		t.Errorf("auth.refresh_ttl_hours default = %d, want 168", o.Auth.RefreshTTLHours)
	}
	if o.Audit.FallbackTenant != "t-default" {
		t.Errorf("audit.fallback_tenant default = %q, want t-default", o.Audit.FallbackTenant)
	}
}

// 关键行为保持：零配置必须合法可启动。
func TestNewServerOptions_ZeroConfigIsValid(t *testing.T) {
	o := NewServerOptions()
	if err := o.Validate(); err != nil {
		t.Fatalf("zero-config must be valid, got %v", err)
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
		{"TTL 为零", func(o *ServerOptions) { o.Auth.AccessTTLMinutes = 0 }, true},
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
// （login 段的聚合用例迁往 authd/config_test.go；此处以 TTL+SSE 两个独立错误源
// 保持「同一条错误报全」的覆盖。）
func TestServerOptions_ValidateAggregatesAllErrors(t *testing.T) {
	o := NewServerOptions()
	o.Auth.AccessTTLMinutes = 0
	o.SSE.Addr = ":8090"

	err := o.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want aggregated error")
	}
	msg := err.Error()
	for _, frag := range []string{"access_ttl", "sse.addr"} {
		if !strings.Contains(msg, frag) {
			t.Errorf("aggregated error %q missing %q", msg, frag)
		}
	}
}
