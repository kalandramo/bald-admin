package e2e

// t_w1_ratelimit_e2e_test.go —— Wave 1a 登录限流 e2e（RED 起点）。
//
// 验收（计划 1.2，对齐源项目 LoginRateLimiter 的 fail-open 语义）：
//  1. 连续错误凭据触发限流 → 后续请求 429（而非继续 401）
//  2. 限流是**按客户端维度**的：另一 IP/用户名不受影响
//  3. 限流器未配置（nil）时不阻断登录（禁用态）
//  4. Redis/后端不可用时 fail-open（不阻断登录）——由单元测试覆盖，
//     此处覆盖「limiter 为 nil」的降级路径
//
// 与源项目的语义对齐：源用 Redis 计数（LoginRateLimiter.CheckAndIncr），
// 本实现用 bald 的 ratelimit/tokenbucket（进程内），故 fail-open 的触发条件
// 从「Redis 不可用」变为「限流器未注入」——这是能力轴替换带来的语义映射，
// 已在 Wave 1 交付报告记录。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/ratelimit/tokenbucket"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// startLoginRESTWithLimiter 起真实 gin 引擎 + 注入指定限流器（nil = 禁用态）。
// 形态对齐 startTenantREST（httptest.NewServer + t.Cleanup）。
func startLoginRESTWithLimiter(t *testing.T, limiter *tokenbucket.Limiter) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	if limiter != nil {
		authBiz.SetLoginLimiter(limiter)
	}
	e := gingonic.New()
	apiserver.RegisterRoutes(e, &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoginRateLimit_ExceededReturns429 连续错误凭据超过突发额度 → 429。
func TestLoginRateLimit_ExceededReturns429(t *testing.T) {
	// rate=1/s, burst=2 → 前 2 次放行（401），第 3 次起拒绝（429）。
	lim, err := tokenbucket.New(1, 2)
	if err != nil {
		t.Fatalf("tokenbucket.New: %v", err)
	}
	t.Cleanup(func() { _ = lim.Close() })
	base := startLoginRESTWithLimiter(t, lim)

	body := map[string]any{"username": "admin", "password": "wrong-password"}

	// 前 2 次：限流放行 → 走到凭据校验 → 401。
	for i := 0; i < 2; i++ {
		if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login", body); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d, want 401 (within burst)", i+1, code)
		}
	}
	// 第 3 次：超出突发额度 → 429（限流器在凭据校验之前拦截）。
	if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login", body); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 3: status=%d, want 429 (rate limited)", code)
	}
}

// TestLoginRateLimit_NilLimiterDoesNotBlock 限流器未注入 → 不阻断（禁用态降级）。
func TestLoginRateLimit_NilLimiterDoesNotBlock(t *testing.T) {
	base := startLoginRESTWithLimiter(t, nil)

	body := map[string]any{"username": "admin", "password": "wrong-password"}
	// 连续多次都应是 401（凭据错），而非 429——证明 nil 限流器不阻断。
	for i := 0; i < 5; i++ {
		if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login", body); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d, want 401 (limiter disabled must not block)", i+1, code)
		}
	}
}

// TestLoginRateLimit_DoesNotBlockValidLogin 限流额度内正常登录不受影响。
func TestLoginRateLimit_DoesNotBlockValidLogin(t *testing.T) {
	lim, err := tokenbucket.New(1, 5)
	if err != nil {
		t.Fatalf("tokenbucket.New: %v", err)
	}
	t.Cleanup(func() { _ = lim.Close() })
	base := startLoginRESTWithLimiter(t, lim)

	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"})
	if code != http.StatusOK {
		t.Fatalf("valid login status=%d body=%s, want 200", code, raw)
	}
}
