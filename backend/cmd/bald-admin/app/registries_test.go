package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"

	bconf "github.com/kalandramo/bald/bconf"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
)

// TestApplyObservabilityDefaults_GatewayAddr 验证 gateway 地址的**配置优先级契约**：
// 显式 `server.gateway` 段 > env `BALD_GATEWAY_ADDR` > 缺省 `:8081`。
//
// 被测对象：本包 `registries.go` 的 `applyObservabilityDefaults`（测试文件与之同名就近）。
// env/flag 值经该函数从 `options.NewServerOptions()`（唯一默认值来源）合成到契约段，
// 显式配置源已配该段时**不被合成值覆盖**。
func TestApplyObservabilityDefaults_GatewayAddr(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Unsetenv("BALD_GATEWAY_ADDR")
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, ":8081", cfg.GetServer().GetGateway().GetAddr())
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("BALD_GATEWAY_ADDR", ":18081")
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, ":18081", cfg.GetServer().GetGateway().GetAddr())
	})
	t.Run("explicit section wins", func(t *testing.T) {
		os.Unsetenv("BALD_GATEWAY_ADDR")
		svrOpts := options.NewServerOptions()
		cfg := bconf.NewBootstrap()
		cfg.GetServer().Gateway = &bootstrapv1.Server_Gateway{Addr: ":28081"}
		require.NoError(t, applyObservabilityDefaults(cfg, svrOpts))
		assert.Equal(t, ":28081", cfg.GetServer().GetGateway().GetAddr(),
			"显式 server.gateway 段必须优先于 env 合成值")
	})
}
