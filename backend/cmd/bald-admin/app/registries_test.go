package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"

	bconf "github.com/kalandramo/bald/bconf"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
)

// TestApplyObservabilityDefaults_GatewayAddr 验证 gateway 地址的**合成优先级契约**：
// 显式 `server.gateway` 段 > 合成缺省（ServerOptions 的 :8081）。
//
// 被测对象：本包 `registries.go` 的 `applyObservabilityDefaults`（测试文件与之同名就近）。
//
// W3 变更：本测试原先还覆盖「env BALD_GATEWAY_ADDR 覆盖缺省」——该 env 通道
// 已退役（短名会被框架 env 层截获成契约不存在的键，且优先级与装载链不一致）。
// 改用框架路径名 `BALD_ADMIN_SERVER_GATEWAY_ADDR`，其优先级由装载链处理，
// 故不再由本函数的合成逻辑断言（见 appkit/config 的 env 层测试）。
func TestApplyObservabilityDefaults_GatewayAddr(t *testing.T) {
	t.Run("合成缺省", func(t *testing.T) {
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, ":8081", cfg.GetServer().GetGateway().GetAddr())
	})
	t.Run("显式段优先（不被合成值覆盖）", func(t *testing.T) {
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		cfg.GetServer().Gateway = &bootstrapv1.Server_Gateway{Addr: ":28081"}
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, ":28081", cfg.GetServer().GetGateway().GetAddr(),
			"显式 server.gateway 段必须优先于合成缺省值")
	})
	t.Run("metrics 段缺省时合成 prometheus", func(t *testing.T) {
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		require.NotNil(t, cfg.GetMetrics())
		assert.Equal(t, "prometheus", cfg.GetMetrics().GetType())
		// addr 留空 → 框架缺省 :9091（appkit.StartMetricsServer 内回退）。
		assert.Empty(t, cfg.GetMetrics().GetPrometheus().GetAddr(),
			"合成值不应写死端口，交由框架缺省 :9091")
	})
	t.Run("显式 metrics 段不被覆盖", func(t *testing.T) {
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		cfg.Metrics = &bootstrapv1.Metrics{
			Type:       "otlp",
			Prometheus: &bootstrapv1.Metrics_Prometheus{Addr: ":19091"},
		}
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, "otlp", cfg.GetMetrics().GetType())
		assert.Equal(t, ":19091", cfg.GetMetrics().GetPrometheus().GetAddr())
	})
	t.Run("cron 段缺省时合成", func(t *testing.T) {
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.NotNil(t, cfg.GetServer().GetCron())
	})
}
