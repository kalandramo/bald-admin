package app

// assembly_middleware_test.go —— 契约中间件段（server.http.middleware.*）
// 经 bundle.FromMiddleware 接入生产装配路径的端到端验证。
//
// ## 为什么写在这里
//
// 与 assembly_e2e_test.go 同一动机：驱动**真实**的 buildApp 装配路径，而非
// 自建 bundle。若有人把 FromMiddleware 从 buildApp 里摘掉、或链序回退，这里会红。
//
// ## 验证什么
//
// 契约段 → FromMiddleware → bundle → gin 链。断言装配后的真实服务在 HTTP 请求上
// 体现契约配置的行为（响应头），而非「函数被调用过」。

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

	bconf "github.com/kalandramo/bald/bconf"
	"github.com/kalandramo/bald/pkg/appkit"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
)

const middlewareTestHTTPAddr = ":18199"

// middlewareTestConfig 与原 e2e 配置同构，**唯一差异是加了 server.http.middleware 段**
// ——故任何行为差异都可归因于该段。
const middlewareTestConfig = `config:
  file:
    path: "configs/bald-admin.yaml"
    format: "yaml"
    watch: false
server:
  http:
    addr: ":18199"
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
  grpc:  { addr: ":19199" }
log: { level: "error", format: "json" }
audit:
  backends: "store"
  fallback_tenant: "t-default"
database:
  sql:
    driver: "sqlite"
    source: "probe-mw.db"
    migrate: true
`

// setupAssemblyWithConfig 把配置写入临时工作目录并切到该目录（与既有 e2e 同款：
// buildApp 内 WithConfigFile(configFileDefault) 与 preloadBootstrap 均以 cwd
// 解析相对路径）。返回已构造但未启动的 AppKit。
func setupAssemblyWithConfig(t *testing.T, cfgYAML string) (*appkit.AppKit, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(cfgYAML), 0o644))
	t.Chdir(dir)

	bootstrapCfg := bconf.NewBootstrap()
	bootstrapCfg.GetApp().Name = "bald-admin-mw-e2e"
	bootstrapCfg.GetApp().Version = "v0.1.0"
	svrOpts := options.NewServerOptions()

	cfgReg, err := preloadBootstrap(bootstrapCfg)
	require.NoError(t, err, "配置源预装载")
	require.NoError(t, applyObservabilityDefaults(bootstrapCfg, svrOpts))

	app, _, err := buildApp(bootstrapCfg, svrOpts, cfgReg)
	return app, err
}

// TestAssemblyPath_MiddlewareFromConfig 契约 middleware 段经生产装配路径生效。
//
// 断言（HTTP 可观察行为，非内部状态）：
//   - request_id.header_name=X-Corr-Id → 响应带 X-Corr-Id 头（且不带默认头）；
//   - cors 各字段 → 响应带对应 Access-Control-* 头（含 exposed_headers/max_age）。
func TestAssemblyPath_MiddlewareFromConfig(t *testing.T) {
	app, err := setupAssemblyWithConfig(t, middlewareTestConfig)
	require.NoError(t, err, "buildApp 装配")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- app.Run(ctx) }()

	require.Eventually(t, func() bool {
		c, derr := net.DialTimeout("tcp", middlewareTestHTTPAddr, 200*time.Millisecond)
		if derr != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 30*time.Second, 200*time.Millisecond, "服务未在超时内监听")

	// 请求**必须由 gin 处理**才能观察到全局中间件：
	//   - /healthz 由 appkit 的探针 ServeMux 在 gin 之外直接响应（withProbes），
	//     不经过 gin 链，无法用来验证中间件；
	//   - 故用 gin 的 NoRoute 路径（未匹配任何路由）：全局中间件仍会执行，
	//     且不会落入 /v1 路由组（该组自带 mid.CORS(mid.DefaultCORS())，会覆盖
	//     全局 CORS 配置——见测试末尾注释）。
	req, rerr := http.NewRequest(http.MethodGet, "http://localhost"+middlewareTestHTTPAddr+"/middleware-probe", nil)
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

	cancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err, "Run 应正常停机（含 http-middleware 停机 Effect）")
	case <-time.After(30 * time.Second):
		t.Fatal("app 未在停机超时内退出")
	}
}

// TestAssemblyPath_InvalidMiddlewareFailsFast 显式声明非法中间件值 → 装配期报错
// （而非静默忽略配置）。
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
log: { level: "error", format: "json" }
`
	_, err := setupAssemblyWithConfig(t, badCfg)
	require.Error(t, err, "非法 timeout 段应使装配 fail-fast")
	assert.Contains(t, err.Error(), "timeout", "错误信息应指明 timeout 段")
}
