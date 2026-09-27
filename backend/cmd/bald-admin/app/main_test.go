package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
)

// TestGatewayAddr_M6 验证 M6 生产化：gateway 地址可经 env BALD_GATEWAY_ADDR 配置。
//
// W2：env 读取收敛到 options.NewServerOptions()（唯一默认值来源），故断言改为
// 「经 NewServerOptions 构造后 gatewayAddr 返回期望值」——行为语义不变。
func TestGatewayAddr_M6(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Unsetenv("BALD_GATEWAY_ADDR")
		assert.Equal(t, ":8081", gatewayAddr(options.NewServerOptions()))
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("BALD_GATEWAY_ADDR", ":18081")
		assert.Equal(t, ":18081", gatewayAddr(options.NewServerOptions()))
	})
}
