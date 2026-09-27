package app

// assembly_e2e_test.go —— Wave 4.3：**端到端装配路径**测试。
//
// ## 为什么必须有它
//
// 历史上两个真实的安全 bug 之所以能躲过测试，都是同一个原因：e2e 自行拼装
// BizSet / 手动赋包级变量，**绕过了 main 的装配路径**。
//   - M10.1：构造期把 nil 认证器传给 RegisterRoutes，gin 认证对 nil 退化为
//     空操作 → HTTP 认证在**生产路径**一直未生效；
//   - Wave 1d：同理导致登出吊销检查在生产路径静默失效。
//
// 本测试驱动 `buildApp`（生产装配入口），断言「服务真实监听后，认证器/授权器/
// 仓储均已就绪且非 nil」——若装配顺序回退（如又把 biz 构造挪回构造期、或路由
// 注册早于 TokenStore 构造），这里会红。
//
// ## 就绪判据为什么用「端口可连」
//
// 服务监听发生在 beforeStart 链**之后**（链内依次：InitBridges → 业务装配
// → 路由/服务注册 → server 构造/监听）。故「端口可连」是「完整装配链已跑完」
// 的确定性信号——比轮询某个包的变量（会因部分赋值而假早）可靠。

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bconf "github.com/kalandramo/bald/bconf"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
	"github.com/kalandramo/bald-admin/internal/bootstrap"
)

// assemblyTestHTTPAddr 是装配测试的固定 HTTP 地址（非 :0——测试需确定性地
// 轮询端口就绪；选高位端口避免与开发环境冲突）。
const assemblyTestHTTPAddr = ":18099"

// sqliteTestConfig 是不依赖任何远程服务的装配测试配置（剥离 nacos/otlp/minio，
// 数据库走 SQLite 文件）。config.file.path 指向自身——契约 config 段是引导信息，
// 与 appkit 的 --config 两阶段装载同源。
const sqliteTestConfig = `config:
  file:
    path: "configs/bald-admin.yaml"
    format: "yaml"
    watch: false
server:
  http: { addr: ":18099" }
  grpc:  { addr: ":19099" }
log: { level: "error", format: "json" }
audit:
  backends: "store"
  fallback_tenant: "t-default"
database:
  sql:
    driver: "sqlite"
    source: "probe.db"
    migrate: true
`

// TestAssemblyPath_ReadyAfterStart 驱动真实装配路径，断言服务监听后
// 认证器/授权器/仓储均已构造（非 nil）。
//
// RED 场景（本测试要防的回退）：若 InitializeBiz 又被挪回构造期（早于
// InitBridges），或 biz 恢复「请求期读包级变量」，则装配出的是 nil 快照——
// 本测试将在服务起来后观察到 nil 而失败。
func TestAssemblyPath_ReadyAfterStart(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(sqliteTestConfig), 0o644))
	// buildApp → newApp 内 WithConfigFile(configFileDefault) 用相对路径，preloadBootstrap
	// 同样以 configFileDefault 解析 os.Args——切到临时目录使二者都命中。
	t.Chdir(dir)

	// 装配前置：构造 bootstrap 契约（与 serveRunE 同款最小化）。
	bootstrapCfg := bconf.NewBootstrap()
	bootstrapCfg.GetApp().Name = "bald-admin-assembly-e2e"
	bootstrapCfg.GetApp().Version = "v0.1.0"
	svrOpts := options.NewServerOptions()

	cfgReg, err := preloadBootstrap(bootstrapCfg)
	require.NoError(t, err, "配置源预装载")
	require.NoError(t, applyObservabilityDefaults(bootstrapCfg, svrOpts))

	app, _, err := buildApp(bootstrapCfg, svrOpts, cfgReg)
	require.NoError(t, err, "buildApp 装配")

	// 启动：Run 执行 beforeStart 链（InitBridges → 业务装配 → 路由/服务注册 → 监听）。
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- app.Run(ctx) }()

	// 就绪判据：端口可连（监听发生在整条装配链之后）。
	require.Eventually(t, func() bool {
		c, derr := net.DialTimeout("tcp", assemblyTestHTTPAddr, 200*time.Millisecond)
		if derr != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 30*time.Second, 200*time.Millisecond, "服务未在超时内监听（装配链可能失败）")

	// —— 装配不变量断言（此刻完整装配链已跑完）——
	assert.NotNil(t, bootstrap.Authenticator, "认证器应已构造（InitBridges 在装配链内执行）")
	assert.NotNil(t, bootstrap.Authorizer, "授权器应已构造")
	assert.NotNil(t, bootstrap.Signer, "签发器应已构造")
	assert.NotNil(t, bootstrap.UserStore, "用户仓储应已构造")
	assert.NotNil(t, bootstrap.SecretStore, "Secret 仓储应已构造")

	// 停机并确认无装配错误。
	cancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err, "Run 应以正常停机退出，而非装配错误")
	case <-time.After(30 * time.Second):
		t.Fatal("app 未在停机超时内退出")
	}
}
