package app

// assembly_middleware_test.go —— 契约中间件段（server.http.middleware.*）
// 经生产装配路径接入的端到端验证。
//
// ## 为什么写在这里
//
// 与 assembly_e2e_test.go 同一动机：驱动**真实的** Serve + appOptions 装配路径，
// 而非自建 bundle。若有人把 FromMiddleware 从 BeforeStart 里摘掉、或链序回退，
// 这里会红。
//
// ## 验证什么
//
// 契约段 → FromMiddleware → bundle → gin 链。断言装配后的真实服务在 HTTP 请求上
// 体现契约配置的行为（响应头），而非「函数被调用过」。
//
// ## 批 2 时序变化（断言不变）
//
// 中间件段原在构造期读预装载契约装配；批 2 起 Serve 内部持有契约，装配移到
// BeforeStart（装载链后、server 构造前）——仍先于监听，非法值依旧启动期报错
//（TestAssemblyPath_InvalidMiddlewareFailsFast 钉住），且现在吃到四源终值。

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald/pkg/appkit"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
)

// middlewareTestConfigFor 与 e2e 配置同构，**唯一差异是加了 server.http.middleware 段**
// ——故任何行为差异都可归因于该段。
//
// 端口由调用方传入动态空闲端口（见 testaddr_test.go 的根因说明）。
func middlewareTestConfigFor(httpPort, grpcPort string) string {
	return `config:
  file:
    path: "configs/bald-admin.yaml"
    format: "yaml"
    watch: false
server:
  http:
    addr: "` + httpPort + `"
    middleware:
      recovery:
        stack_trace: false
      request_id:
        header_name: "X-Corr-Id"
      cors:
        allowed_origins: ["https://console.example"]
        allowed_methods: ["GET", "POST"]
        allowed_headers: ["Content-Type", "Authorization"]
        exposed_headers: ["X-Total-Count"]
        allow_credentials: true
        max_age: 1800
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
    source: "probe-mw.db"
    migrate: true
biz:
  audit:
    fallback_tenant: "t-default"
`
}

// setupAssemblyWithConfig 把配置写入临时工作目录并切到该目录（与 e2e 同款：
// Serve 以 ConfigFile 声明的相对路径装载配置）。返回驱动装配的 Option 清单——
// 由调用方经 appkit.Serve 启动（生产路径：声明与运行同源）。
func setupAssemblyWithConfig(t *testing.T, cfgYAML string) []appkit.Option {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(cfgYAML), 0o644))
	t.Chdir(dir)
	return appOptions(options.NewServerOptions())
}

// serveUntilReady 启动 Serve 并等待端口可连；返回停机函数与错误通道。
func serveUntilReady(t *testing.T, opts []appkit.Option, httpAddr string) (func(), <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- appkit.Serve(ctx, opts...) }()
	require.Eventually(t, func() bool {
		c, derr := net.DialTimeout("tcp", httpAddr, 200*time.Millisecond)
		if derr != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 30*time.Second, 200*time.Millisecond, "服务未在超时内监听（装配链可能失败）")
	stop := func() {
		cancel()
		select {
		case err := <-runErr:
			assert.NoError(t, err, "Serve 应正常停机")
		case <-time.After(30 * time.Second):
			t.Fatal("app 未在停机超时内退出")
		}
	}
	return stop, runErr
}

// TestAssemblyPath_MiddlewareFromConfig 契约 middleware 段经生产装配路径生效。
//
// 断言（HTTP 可观察行为，非内部状态）：
//   - request_id.header_name=X-Corr-Id → 响应带 X-Corr-Id 头（且不带默认头）；
//   - cors 各字段 → 响应带对应 Access-Control-* 头（含 exposed_headers/max_age）。
func TestAssemblyPath_MiddlewareFromConfig(t *testing.T) {
	httpAddr := mustPort(t, freeTCPAddr(t))
	opts := setupAssemblyWithConfig(t,
		middlewareTestConfigFor(httpAddr, mustPort(t, freeTCPAddr(t))))

	stop, _ := serveUntilReady(t, opts, httpAddr)

	// 请求**必须由 gin 处理**才能观察到全局中间件：
	//   - /healthz 由 appkit 的探针 ServeMux 在 gin 之外直接响应（withProbes），
	//     不经过 gin 链，无法用来验证中间件；
	//   - 故用 gin 的 NoRoute 路径（未匹配任何路由）：全局中间件仍会执行，
	//     且不会落入 /v1 路由组（该组自带 mid.CORS(mid.DefaultCORS())，会覆盖
	//     全局 CORS 配置——见测试末尾注释）。
	req, rerr := http.NewRequest(http.MethodGet, "http://localhost"+httpAddr+"/middleware-probe", nil)
	require.NoError(t, rerr)
	resp, herr := http.DefaultClient.Do(req)
	require.NoError(t, herr, "请求应可达")
	defer resp.Body.Close()

	// request_id.header_name 生效。
	assert.NotEmpty(t, resp.Header.Get("X-Corr-Id"),
		"middleware.request_id.header_name=X-Corr-Id 未生效")
	assert.Empty(t, resp.Header.Get("X-Request-ID"),
		"自定义头名生效时不应再写默认头 X-Request-ID")

	// cors 段生效。
	assert.Equal(t, "https://console.example", resp.Header.Get("Access-Control-Allow-Origin"),
		"middleware.cors.allowed_origins 未生效")
	assert.Equal(t, "GET, POST", resp.Header.Get("Access-Control-Allow-Methods"),
		"middleware.cors.allowed_methods 未生效")
	assert.Equal(t, "X-Total-Count", resp.Header.Get("Access-Control-Expose-Headers"),
		"middleware.cors.exposed_headers 未生效")
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"),
		"middleware.cors.allow_credentials 未生效")
	assert.Equal(t, "1800", resp.Header.Get("Access-Control-Max-Age"),
		"middleware.cors.max_age 未生效")

	stop()
}

// TestAssemblyPath_MiddlewareOnBusinessRoute 配置的 cors 段必须在**真实业务
// 路由**（/v1/*，此前由路由组自带 mid.CORS(mid.DefaultCORS()) 覆盖）上生效。
//
// 回归场景（本测试要防的回退）：若有人再把 mid.CORS(...) 挂回某个路由组，
// 该组响应头会被硬编码默认值（Allow-Origin: *）覆盖，本测试即红——这正是
// 移除组内 CORS、统一走全局链的目的。
func TestAssemblyPath_MiddlewareOnBusinessRoute(t *testing.T) {
	httpAddr := mustPort(t, freeTCPAddr(t))
	opts := setupAssemblyWithConfig(t,
		middlewareTestConfigFor(httpAddr, mustPort(t, freeTCPAddr(t))))

	stop, _ := serveUntilReady(t, opts, httpAddr)

	// /v1/ping 是真实业务路由（此前该组自带 CORS，是覆盖点）。
	req, rerr := http.NewRequest(http.MethodGet, "http://localhost"+httpAddr+"/v1/ping", nil)
	require.NoError(t, rerr)
	resp, herr := http.DefaultClient.Do(req)
	require.NoError(t, herr)
	defer resp.Body.Close()

	// 配置值必须生效——若被组内 mid.DefaultCORS() 覆盖，这里会是 "*"。
	assert.Equal(t, "https://console.example", resp.Header.Get("Access-Control-Allow-Origin"),
		"业务路由 /v1/ping 上配置的 cors.allowed_origins 未生效（疑又被组内 CORS 覆盖）")
	assert.Equal(t, "X-Total-Count", resp.Header.Get("Access-Control-Expose-Headers"),
		"业务路由上配置的 cors.exposed_headers 未生效")

	stop()
}

// TestAssemblyPath_InvalidMiddlewareFailsFast 显式声明非法中间件值 → 启动期报错
//（BeforeStart 内 fail-fast，先于监听——而非静默忽略配置）。
func TestAssemblyPath_InvalidMiddlewareFailsFast(t *testing.T) {
	const badCfg = `config:
  file:
    path: "configs/bald-admin.yaml"
    format: "yaml"
    watch: false
server:
  http:
    addr: ":18299"
    middleware:
      timeout:
        default_timeout_ms: 0
logger:
  backends:
    - type: "slog"
      slog:
        level: "error"
        format: "json"
`
	_opts := setupAssemblyWithConfig(t, badCfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := appkit.Serve(ctx, _opts...)
	require.Error(t, err, "非法 timeout 段应使启动 fail-fast（BeforeStart，先于监听）")
	assert.Contains(t, err.Error(), "timeout", "错误信息应指明 timeout 段")
}
