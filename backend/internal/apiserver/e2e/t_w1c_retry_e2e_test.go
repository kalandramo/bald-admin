package e2e

// t_w1c_retry_e2e_test.go —— Wave 1c 登录 DB 查询重试 e2e（RED 起点）。
//
// 验收（计划 1.1 的**场景修正**，见 Wave 1a/1b 提交说明）：
// 计划原提「retry 包裹 JWT 验签失败重试」是反模式——bald/retry 的包注释写明
// 它面向 **transient failures（瞬时故障）**，而 JWT 验签是确定性函数
//（同 token + 同密钥 → 必然同样失败），重试毫无意义。正确的重试对象是
// 有瞬时故障的真实 I/O：这里是 Login 的 DB 查询（与 Wave 1b 的熔断同一位置）。
//
// retry 与熔断在此**天然组合**（retry 包注释明说 composable with circuit
// breakers）：retry 处理「偶发抖动」（重试可恢复），熔断处理「持续故障」
//（重试无望时快速失败，避免雪崩）。
//
// 关键边界（同熔断）：store.ErrNotFound（用户不存在）是正常业务结果，
// **不应重试**——重试一个不存在的用户只是浪费 3 次 DB 往返。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/store"
	"github.com/kalandramo/bald/retry"
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

// startLoginRESTWithRetrier 起真实 gin 引擎 + 注入指定重试器。
func startLoginRESTWithRetrier(t *testing.T, r *retry.Retrier) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	if r != nil {
		authBiz.SetLoginRetrier(r)
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

// TestLoginRetrier_DisabledByDefault 未注入重试器 → 单次查询（禁用态）。
func TestLoginRetrier_DisabledByDefault(t *testing.T) {
	base := startLoginRESTWithRetrier(t, nil)
	// 正常登录不受影响。
	if code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"}); code != http.StatusOK {
		t.Fatalf("valid login status=%d with nil retrier, want 200", code)
	}
}

// TestLoginRetrier_DoesNotRetryNotFound 用户不存在（NotFound）**不应重试**——
// 重试确定性失败只是浪费 DB 往返。
//
// 验证方式：注入一个「计数器包装」的重试器观察不到次数，故改为验证
// 语义正确性——NotFound 走完后仍是 401（而非被重试逻辑改变成别的状态），
// 且响应及时（未叠加 3 次退避的延迟）。
func TestLoginRetrier_DoesNotRetryNotFound(t *testing.T) {
	// 带明显退避（每次 200ms）——若 NotFound 被重试，响应会明显变慢。
	r := retry.New(
		retry.WithMaxAttempts(3),
		retry.WithBackoff(retry.ExponentialBackoff{Initial: 200 * time.Millisecond, Factor: 2}),
		retry.WithClassifier(authbiz.RetryOnTransientDBError), // 生产同款分类器
	)
	base := startLoginRESTWithRetrier(t, r)

	start := time.Now()
	code, _ := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "no-such-user", "password": "whatever"})
	elapsed := time.Since(start)

	if code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401 (NotFound is a normal 401)", code)
	}
	// 若被重试 3 次，最少耗时 200ms+400ms=600ms。未重试则应远低于此。
	if elapsed > 300*time.Millisecond {
		t.Fatalf("NotFound took %v — looks retried (should NOT retry a deterministic miss); "+
			"with 200ms+400ms backoff, a retried path would exceed 600ms", elapsed)
	}
	t.Logf("NotFound 路径耗时 %v（未重试，符合预期）", elapsed)
}

// TestLoginRetrier_RetriesTransientThenSucceeds 分类器语义验证：
// RetryOnTransientDBError 对 NotFound 返回 false（不重试），对其他 error 返回 true。
func TestLoginRetrier_ClassifierSemantics(t *testing.T) {
	if authbiz.RetryOnTransientDBError(store.ErrNotFound) {
		t.Fatal("classifier must NOT retry store.ErrNotFound (deterministic miss)")
	}
	if !authbiz.RetryOnTransientDBError(errors.New("connection refused")) {
		t.Fatal("classifier must retry transient DB errors (connection refused)")
	}
	if !authbiz.RetryOnTransientDBError(errors.New("context deadline exceeded")) {
		t.Fatal("classifier must retry timeouts")
	}
	if authbiz.RetryOnTransientDBError(nil) {
		t.Fatal("classifier must not retry nil error")
	}
}
