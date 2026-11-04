package app

// assembly_e2e_test.go —— **端到端装配路径**测试。
//
// ## 为什么必须有它
//
// 历史上两个真实的安全 bug 之所以能躲过测试，都是同一个原因：e2e 自行拼装
// 依赖 / 手动赋包级变量，**绕过了 main 的装配路径**。
//   - 构造期把 nil 认证器传给 RegisterRoutes，gin 认证对 nil 退化为
//     空操作 → HTTP 认证在**生产路径**一直未生效；
//   - 同理导致登出吊销检查在生产路径静默失效。
//
// 本测试驱动 appkit.Serve + appOptions（生产装配声明），断言「服务真实监听后，
// 认证器/授权器/仓储均已就绪且非 nil」——若装配顺序回退（如又把 biz 构造挪回
// 构造期、或路由注册早于 TokenStore 构造），这里会红。
//
// ## 就绪判据为什么用 AfterStart 运行态信号
//
// 服务监听发生在 beforeStart 链**之后**（链内依次：InitBridges → 业务装配
// → 路由/服务注册 → server 构造/监听）。框架在「每个 server 的端点定型且注册
// 完成」之后才跑 AfterStart 钩子，所以那个信令本身就是「完整装配链已跑完」的
// 确定性判据；端口可连退成后续断言（只回答「监听确实建立了」），不再用来猜就绪。
//
// 曾经的形状：只轮询 HTTP 端口时判据弱于「装配完成」——gRPC 的 `Endpoint()`
// 在 `net.Listen` 完成前返回空串，appkit 的端点等待因此仍未结束，此时 `cancel()`
// 落在启动窗口内；本仓库当时消费的 `bald/pkg v0.2.1` 把这种取消当装配错误返回，
// 共享 runner 上实测红过一次（`Serve` 返回
// `appkit: wait for endpoints canceled: context canceled`，本地 `-count=30`
// 复现不了）。框架侧自 `pkg v0.2.2` 起按正常停机退出（bald 风险评审 §4.4），
// 本仓库已升到该版本；但判据也不依赖「取消恰好没事」——先拿运行态信号再停机，
// 那个窗口根本不参与。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald/pkg/appkit"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
	"github.com/kalandramo/bald-admin/internal/bootstrap"
)

// sqliteTestConfigFor 生成「不依赖任何远程服务」的装配测试配置（剥离 nacos/otlp/minio，
// 数据库走 SQLite 文件）。config.file.path 指向自身——契约 config 段是引导信息，
// 与 Serve 内部 --config 两阶段装载同源。
//
// httpPort/grpcPort 由调用方传入**动态空闲端口**（见 testaddr_test.go 的根因说明：
// 硬编码端口会被残留进程冒名顶替，导致就绪判据失真）。
func sqliteTestConfigFor(httpPort, grpcPort string) string {
	return `app:
  name: "bald-admin"
config:
  file:
    path: "configs/bald-admin.yaml"
    format: "yaml"
    watch: false
server:
  http: { addr: "` + httpPort + `" }
  grpc:  { addr: "` + grpcPort + `" }
logger:
  backends:
    - type: "slog"
      slog:
        level: "error"
        format: "json"
audit:
  backends: "store"
database:
  sql:
    driver: "sqlite"
    source: "probe.db"
    migrate: true
biz:
  audit:
    fallback_tenant: "t-default"
`
}

// TestAssemblyPath_ReadyAfterStart 驱动真实装配路径（Serve + appOptions），
// 断言服务监听后认证器/授权器/仓储均已构造（非 nil）。
//
// RED 场景（本测试要防的回退）：若 biz 构造又被挪回 InitBridges 之前（如已删
// 的 InitializeBiz 形态），或 biz 恢复「请求期读包级变量」，则装配出的是 nil 快照——
// 本测试将在服务起来后观察到 nil 而失败。
func TestAssemblyPath_ReadyAfterStart(t *testing.T) {
	// 动态端口（见 testaddr_test.go：固定端口会被残留进程冒名顶替）。
	httpAddr := freeTCPAddr(t)
	grpcAddr := freeTCPAddr(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(sqliteTestConfigFor(mustPort(t, httpAddr), mustPort(t, grpcAddr))), 0o644))
	// Serve 内部以 ConfigFile 声明的相对路径装载配置——切到临时目录使命中。
	t.Chdir(dir)

	svrOpts := options.NewServerOptions()

	// 启动：Serve = --config 解析 + 契约预装载 + 段驱动装配 + Run
	//（beforeStart 链：InitBridges → 业务装配 → 路由/服务注册 → 监听）。
	// 运行态信令只加在测试的 opts 上，不进 appOptions（生产装配声明不含测试信令）。
	ctx, cancel := context.WithCancel(context.Background())
	opts, booted := withBootSignal(appOptions(svrOpts))
	runErr := make(chan error, 1)
	go func() { runErr <- appkit.Serve(ctx, opts...) }()

	// 就绪：先等 AfterStart 运行态信号（装配链真的跑完了），再确认 HTTP 与 gRPC
	// 两个端口都可连（监听确实建立）。端口为本次运行专属的空闲端口——不会被残留
	// 进程冒名顶替（见 testaddr_test.go）。
	awaitBoot(t, booted, runErr)
	requireDialable(t, httpAddr, grpcAddr)

	// —— 装配不变量断言（此刻完整装配链已跑完）——
	assert.NotNil(t, bootstrap.Authenticator, "认证器应已构造（InitBridges 在装配链内执行）")
	assert.NotNil(t, bootstrap.Authorizer, "授权器应已构造")
	assert.NotNil(t, bootstrap.Signer, "签发器应已构造")
	// 仓储经 InitBridges 返回值建立——本测试走生产装配路径（拿不到返回值），
	// 故经 LastRepositories() 观察。先断言聚合本身非 nil，再解引用其字段
	//（否则聚合为 nil 时断言会 panic 而非给出可读失败信息）。
	br := bootstrap.LastRepositories()
	require.NotNil(t, br, "仓储聚合应已构造（InitBridges 在装配链内执行）")
	assert.NotNil(t, br.User, "用户仓储应已构造")
	assert.NotNil(t, br.Secret, "Secret 仓储应已构造")

	// 停机并确认无装配错误。
	cancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err, "Serve 应以正常停机退出，而非装配错误")
	case <-time.After(30 * time.Second):
		t.Fatal("app 未在停机超时内退出")
	}
}
