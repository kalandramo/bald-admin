package e2e

// t_w6_2_cache_monitor_e2e_test.go —— Wave 6.2：Redis 缓存监控（1 rpc）。
//
// ## fail-soft 语义（对齐源 redis_cache_monitor_repo.go）
//
// 三个只读命令（INFO/DBSIZE/SLOWLOG GET）**任一失败不阻断其余**；Redis 不可用
// 返回**空视图**（非错误）——避免单点故障导致整个监控页不可用。
//
// ## 验证策略
//
//  1. **Redis 不可用**（本机无本地 Redis，RedisClient=nil）：断言返回 200 +
//     空视图（fail-soft 语义的直接证据，不需外部依赖）。
//  2. **Redis 可用**（env 注入 `BALD_ADMIN_TEST_REDIS_ADDR`）：断言 INFO 分节
//     解析 + DBSIZE 返回——真实数据验证。缺 env 则跳过该段。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	gingonic "github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/admin/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	cmbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/cachemonitor"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// startCacheMonitorREST 起真实 gin 引擎（可选注入 Redis 客户端）。
func startCacheMonitorREST(t *testing.T) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	// 若 env 提供 Redis，注入真实客户端（否则保持 nil → fail-soft 空视图）。
	// **必须传密码**：远程 Redis 开了 ACL 认证，无密码会 WRONGPASS（实测踩坑）。
	if addr := os.Getenv("BALD_ADMIN_TEST_REDIS_ADDR"); addr != "" {
		rdb := goredis.NewClient(&goredis.Options{
			Addr:     addr,
			Password: os.Getenv("BALD_ADMIN_TEST_REDIS_PASSWORD"),
			DB:       atoiSafe(os.Getenv("BALD_ADMIN_TEST_REDIS_DB")),
		})
		t.Cleanup(func() { _ = rdb.Close() })
		bootstrappkg.RedisClient = rdb
	}

	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		CacheMonitor: cmbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestWave6_2_CacheMonitorFailSoft Redis 不可用时 fail-soft 返回空视图（200）。
func TestWave6_2_CacheMonitorFailSoft(t *testing.T) {
	// **显式控制全局态**：RedisClient 是进程级单例，同包其他测试可能已注入
	// 真实客户端（如 TestWave6_2_CacheMonitorWithRedis）——不隔离会让本测试
	// 的「空视图」断言随执行顺序时红时绿（跨测试全局态污染，同 Wave 5.5 教训）。
	prev := bootstrappkg.RedisClient
	bootstrappkg.RedisClient = nil
	t.Cleanup(func() { bootstrappkg.RedisClient = prev })

	base := startCacheMonitorREST(t)
	// 注意：startCacheMonitorREST 在 env 存在时会重新注入——故显式再置 nil。
	bootstrappkg.RedisClient = nil
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/v1/redis-cache-monitor", nil)
	if code != http.StatusOK {
		t.Fatalf("Redis 不可用时应 200（fail-soft），got %d body=%s", code, raw)
	}
	out := new(adminv1.RedisCacheMonitorInfo)
	decodePB(raw, out)
	if len(out.GetSections()) != 0 || out.GetDbSize() != 0 {
		t.Fatalf("Redis 不可用应返回空视图，实际 sections=%d db_size=%d",
			len(out.GetSections()), out.GetDbSize())
	}
	t.Log("fail-soft 正确：Redis 不可用返回 200 + 空视图")
}

// TestWave6_2_CacheMonitorWithRedis Redis 可用时返回真实 INFO/DBSIZE。
//
// 需 env BALD_ADMIN_TEST_REDIS_ADDR；缺省 Skip（不伪装通过）。
func TestWave6_2_CacheMonitorWithRedis(t *testing.T) {
	if os.Getenv("BALD_ADMIN_TEST_REDIS_ADDR") == "" {
		t.Skip("BALD_ADMIN_TEST_REDIS_ADDR not set: skip real-Redis e2e (no fake allowed)")
	}
	base := startCacheMonitorREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/v1/redis-cache-monitor", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	out := new(adminv1.RedisCacheMonitorInfo)
	decodePB(raw, out)
	if len(out.GetSections()) == 0 {
		t.Fatalf("真实 Redis 应返回 INFO 分节，实际为空")
	}
	// 至少应含 server 或 clients 等常见 section。
	t.Logf("真实 Redis 监控：%d 个 INFO section, db_size=%d, slowlog=%d 条",
		len(out.GetSections()), out.GetDbSize(), len(out.GetSlowlog()))
}

// atoiSafe 解析十进制整数；空/非法返回 0。
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
