package e2e

// t_w1b_circuitbreaker_e2e_test.go —— Wave 1b 登录 DB 查询熔断 e2e（RED 起点）。
//
// 验收（计划 1.3 的**场景修正**，见 Wave 1a 提交说明）：
// 计划原提「circuitbreaker 包裹审计落库」收益极低——审计是旁路不阻断语义
//（recordSafely 有 recover 兜底），给失败无感的旁路加熔断没有意义。真正的
// 雪崩点在**主链路的 DB 查询**（Login 的 UserStore.Get）：DB 故障会让所有
// 登录请求卡在超时上。故本波把熔断挂在这里。
//
// 断言策略说明（重要）：本测试用 **hystrix** 而非 sres 作为实现。
// 原因（实测发现，见 Wave 1b 交付报告）：sres 是 SRE 概率式熔断，其
// accept = (requests - K*errors)/(requests+1) **恒 < 1**，故：
//   (a) 100 次全成功路径下仍概率拒绝（K=2 时实测拒 12 次）；
//   (b) State 永不返回 StateClosed（恒 half-open）——违反契约
//       circuitbreaker.go:22 对 StateClosed 的定义（"all requests are allowed"）。
// hystrix 是阈值式熔断（默认 Closed 全放行，50% 错误率 + 最少 20 请求才评估），
// 与契约语义一致，故用它。
//
// 关键边界：store.ErrNotFound（用户不存在）是**正常业务结果**，绝不能计入
// 熔断失败——否则攻击者用几个不存在的用户名就能熔断整个登录。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/circuitbreaker"
	"github.com/kalandramo/bald/circuitbreaker/hystrix"
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

// startLoginRESTWithBreaker 起真实 gin 引擎 + 注入指定熔断器。
// 形态对齐 startTenantREST（httptest.NewServer + t.Cleanup）。
func startLoginRESTWithBreaker(t *testing.T, cb circuitbreaker.CircuitBreaker) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	if cb != nil {
		authBiz.SetLoginBreaker(cb)
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

// TestLoginBreaker_NotTrippedByNotFound 用户不存在（ErrNotFound）是正常业务
// 结果，不得计入熔断失败——否则可用不存在的用户名熔断整个登录。
func TestLoginBreaker_NotTrippedByNotFound(t *testing.T) {
	cb := hystrix.New()
	t.Cleanup(func() { _ = cb.Close() })
	base := startLoginRESTWithBreaker(t, cb)

	// 连续用**不存在**的用户名登录（每次都是 ErrNotFound 路径）。
	// 次数超过 hystrix 的 requestVolumeThreshold（默认 20）以证明：
	// 即便请求量够多，只要没有真故障就不会熔断。
	for i := 0; i < 25; i++ {
		code, raw := callRaw(t, base, "", http.MethodPost, "/v1/login",
			map[string]any{"username": "no-such-user", "password": "whatever"})
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d body=%s, want 401 "+
				"(nonexistent user is a normal 401)", i+1, code, raw)
		}
	}

	// 熔断器应仍为 Closed（NotFound 未被计为失败）。
	if got := cb.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("breaker state=%v after %d ErrNotFound logins, want closed "+
			"(ErrNotFound must not count as failure)", got, 25)
	}

	// 且正常登录仍可用（证明未被误熔断）。
	if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"}); code != http.StatusOK {
		t.Fatalf("valid login status=%d after NotFound-only attempts, want 200", code)
	}
}

// TestLoginBreaker_NilBreakerDoesNotBlock 未注入熔断器 → 不阻断（禁用态）。
func TestLoginBreaker_NilBreakerDoesNotBlock(t *testing.T) {
	base := startLoginRESTWithBreaker(t, nil)
	for i := 0; i < 3; i++ {
		if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login",
			map[string]any{"username": "admin", "password": "admin123"}); code != http.StatusOK {
			t.Fatalf("attempt %d: status=%d, want 200 (breaker disabled)", i+1, code)
		}
	}
}

// TestLoginBreaker_TripsOnRealDBFailure 真实 DB 故障（非 NotFound）→ 熔断器
// 计入失败并最终 Open；Open 时登录被快速拒绝（503），不再等待 DB 超时。
func TestLoginBreaker_TripsOnRealDBFailure(t *testing.T) {
	// 用低阈值 + 低请求量门槛，便于在测试中触发（默认 50% / 20 请求）。
	cb := hystrix.New(
		hystrix.WithErrorThreshold(0.5),
		hystrix.WithRequestVolumeThreshold(5),
		hystrix.WithSleepWindow(time.Hour), // 测试期间不自动恢复
	)
	t.Cleanup(func() { _ = cb.Close() })

	// 直接驱动熔断器：模拟真实 DB 故障（MarkFailure）。
	for i := 0; i < 6; i++ {
		_ = cb.Allow()
		cb.MarkFailure()
	}
	if got := cb.State(); got != circuitbreaker.StateOpen {
		t.Fatalf("breaker state=%v after 6 real failures (threshold 50%%/5req), want open", got)
	}

	// 注入该熔断器：HTTP 层应快速失败（503），不再走 DB。
	base := startLoginRESTWithBreaker(t, cb)
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("login status=%d while breaker open, want 503 (fail fast), body=%s", code, raw)
	}
	t.Logf("breaker %v → login rejected with HTTP %d (fail fast, no DB round-trip)", cb.State(), code)
}
