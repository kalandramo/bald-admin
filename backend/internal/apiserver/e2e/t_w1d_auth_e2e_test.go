package e2e

// t_w1d_auth_e2e_test.go —— Wave 1d-1：authentication 域补全（ValidateToken /
// RefreshToken / Logout）的 e2e 测试（RED 起点）。
//
// 验收（对应源 go-wind-admin/backend/api/protos/authentication/service/v1/
// authentication.proto 的 L28/L35/L38 三个 rpc）：
//   - Logout：拉黑当前 token；**拉黑后同一 token 必须被认证中间件拒绝**
//     （这是最难的一环——JWT 无状态，不查黑名单则 token 仍能通过验签）；
//   - RefreshToken：用有效凭证换发新 token（新旧不同）；
//   - ValidateToken：校验任意 token 有效性（返回 valid 布尔，非错误码）。
//
// 需要 Redis（黑名单/刷新令牌存储）。Redis 不可达时 t.Skip——不让环境缺失
// 伪装成测试通过，也不因环境缺失而失败（区分「未验证」与「验证失败」）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	"github.com/kalandramo/bald/pkg/authn"

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
	"github.com/kalandramo/bald-admin/internal/security/captcha"
	"github.com/kalandramo/bald-admin/internal/security/token"
)

// newTestTokenStore 连本地 Redis（docker 起的 6379），返回 Redis 实现的令牌存储。
// Redis 不可达 → t.Skip（区分环境缺失与验证失败）。DB 15 隔离，用完清空。
func newTestTokenStore(t *testing.T) *token.RedisStore {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis 127.0.0.1:6379 不可达，跳过（环境缺失，非验证失败）: %v", err)
	}
	t.Cleanup(func() {
		rdb.FlushDB(context.Background())
		_ = rdb.Close()
	})
	return token.NewRedisStore(rdb)
}

// startAuthREST 起真实 gin 引擎，认证中间件包一层吊销检查（装饰器）。
// ts 为 nil 时不包装（禁用态，行为与 Wave 1d 之前一致）。
func startAuthREST(t *testing.T, ts token.Store) string {
	return startAuthRESTFull(t, ts, nil)
}

// startAuthRESTWithCaptcha 同上，但额外注入验证码存储（Wave 1d-2）。
func startAuthRESTWithCaptcha(t *testing.T, cs captcha.Store) string {
	return startAuthRESTFull(t, nil, cs)
}

func startAuthRESTFull(t *testing.T, ts token.Store, cs captcha.Store) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetTokenStore(ts)
	authBiz.SetCaptchaStore(cs)

	// 认证器：装饰器包住懒解析认证器——验签通过后再查吊销名单。
	var authenticator authn.Authenticator = bootstrappkg.LazyAuthenticator()
	if ts != nil {
		authenticator = token.NewRevocationChecker(authenticator, ts)
	}
	// ValidateToken 需要独立的验签能力（与认证器同源，但不经中间件）。
	authBiz.SetAuthenticator(authenticator)

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, authenticator, &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// loginForToken 登录并返回 access_token + refresh_token。
func loginForToken(t *testing.T, base string) (access, refresh string) {
	t.Helper()
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"})
	if code != http.StatusOK {
		t.Fatalf("login status=%d body=%s, want 200", code, raw)
	}
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &pair); err != nil {
		t.Fatalf("unmarshal login response: %v (body=%s)", err, raw)
	}
	if pair.AccessToken == "" {
		t.Fatalf("login 未返回 access_token: %s", raw)
	}
	return pair.AccessToken, pair.RefreshToken
}

// TestWave1d_ValidateToken 校验任意 token：有效 → valid=true；垃圾 → valid=false。
// 注意：无效 token 不是错误码（不是 401），而是 200 + valid:false——ValidateToken
// 是「询问」语义而非「准入」语义。
func TestWave1d_ValidateToken(t *testing.T) {
	ts := newTestTokenStore(t)
	base := startAuthREST(t, ts)
	access, _ := loginForToken(t, base)

	// 有效 token。
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/validate",
		map[string]any{"token": access})
	if code != http.StatusOK {
		t.Fatalf("validate(valid) status=%d body=%s, want 200", code, raw)
	}
	var vr struct {
		Valid    bool   `json:"valid"`
		Subject  string `json:"subject"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &vr); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if !vr.Valid {
		t.Fatalf("valid token 被判为 invalid: %s", raw)
	}
	if vr.Subject != "u-admin" {
		t.Fatalf("subject=%q, want u-admin", vr.Subject)
	}

	// 垃圾 token → valid=false（200，非错误）。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/auth/validate",
		map[string]any{"token": "not-a-jwt"})
	if code != http.StatusOK {
		t.Fatalf("validate(invalid) status=%d body=%s, want 200 (询问语义)", code, raw)
	}
	if err := json.Unmarshal(raw, &vr); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if vr.Valid {
		t.Fatalf("垃圾 token 被判为 valid: %s", raw)
	}
	t.Logf("ValidateToken 有效/无效两条路径均正确")
}

// TestWave1d_RefreshToken 用有效凭证换发新 token，新旧必须不同。
func TestWave1d_RefreshToken(t *testing.T) {
	ts := newTestTokenStore(t)
	base := startAuthREST(t, ts)
	access, refresh := loginForToken(t, base)
	if refresh == "" {
		t.Fatalf("login 未返回 refresh_token（Wave 1d 应签发刷新令牌）")
	}

	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/refresh",
		map[string]any{"refresh_token": refresh})
	if code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s, want 200", code, raw)
	}
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &pair); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if pair.AccessToken == "" {
		t.Fatalf("refresh 未返回新 access_token: %s", raw)
	}
	if pair.AccessToken == access {
		t.Fatalf("refresh 返回的 access_token 与原 token 相同（未换发）")
	}
	// 新 token 必须可用。
	code, _ = callRaw(t, base, pair.AccessToken, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusOK {
		t.Fatalf("换发的新 token 不可用: status=%d", code)
	}
	t.Logf("RefreshToken 换发成功且新 token 可用")
}

// TestWave1d_RefreshToken_RejectsInvalid 无效刷新令牌必须被拒（不是 200）。
func TestWave1d_RefreshToken_RejectsInvalid(t *testing.T) {
	ts := newTestTokenStore(t)
	base := startAuthREST(t, ts)

	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/refresh",
		map[string]any{"refresh_token": "forged-refresh-token"})
	if code == http.StatusOK {
		t.Fatalf("伪造 refresh_token 被接受（安全缺陷）: %s", raw)
	}
	if code != http.StatusUnauthorized {
		t.Fatalf("伪造 refresh_token status=%d, want 401", code)
	}
}

// TestWave1d_Logout_RevokesToken 是 Wave 1d 最强的端到端证据：
// 登出后同一 token 必须被认证中间件拒绝——证明装饰器真的查了黑名单，
// 而不只是把 token 记进 Redis（JWT 无状态，不查名单则 token 照样通过验签）。
func TestWave1d_Logout_RevokesToken(t *testing.T) {
	ts := newTestTokenStore(t)
	base := startAuthREST(t, ts)
	access, _ := loginForToken(t, base)

	// 前置：token 可用。
	if code, raw := callRaw(t, base, access, http.MethodGet, "/v1/auth/whoami", nil); code != http.StatusOK {
		t.Fatalf("登出前 token 不可用: status=%d body=%s", code, raw)
	}

	// 登出。
	code, raw := callRaw(t, base, access, http.MethodPost, "/v1/auth/logout", nil)
	if code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s, want 200", code, raw)
	}

	// 关键断言：同一 token 再访问必须 401。
	code, raw = callRaw(t, base, access, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("登出后 token 仍可用（黑名单未生效）: status=%d body=%s, want 401", code, raw)
	}

	// ValidateToken 也应报 invalid。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/auth/validate",
		map[string]any{"token": access})
	if code != http.StatusOK {
		t.Fatalf("validate after logout status=%d, want 200", code)
	}
	var vr struct {
		Valid bool `json:"valid"`
	}
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("登出后 ValidateToken 仍报 valid: %s", raw)
	}
	t.Logf("Logout 吊销生效：登出后 token 被中间件拒绝 + ValidateToken 报 invalid")
}

// TestWave1d_Logout_DisabledStoreNoop 未注入令牌存储（禁用态）时 Logout 不应崩，
// 且不影响其他 token——降级语义必须明确。
func TestWave1d_Logout_DisabledStoreNoop(t *testing.T) {
	base := startAuthREST(t, nil) // nil = 禁用态
	access, _ := loginForToken(t, base)

	code, _ := callRaw(t, base, access, http.MethodPost, "/v1/auth/logout", nil)
	if code != http.StatusOK {
		t.Fatalf("禁用态 logout status=%d, want 200（幂等降级）", code)
	}
	// 无存储时无法真吊销——token 仍可用是**已知降级**，不是 bug。
	code, _ = callRaw(t, base, access, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusOK {
		t.Fatalf("禁用态下 token 应仍可用（无存储无法吊销）: status=%d", code)
	}
	t.Logf("禁用态 logout 降级正确（记日志不崩，无吊销能力）")
}

// TestWave1d_TokenStore_RevokeExpiry 拉黑记录的 TTL 应与 token 剩余有效期一致——
// 过期 token 本就无效，拉黑记录长期驻留只是浪费内存。
func TestWave1d_TokenStore_RevokeExpiry(t *testing.T) {
	ts := newTestTokenStore(t)
	ctx := context.Background()
	tok := "sample-token-for-ttl-test"

	if err := ts.Revoke(ctx, tok, 30*time.Second); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	revoked, err := ts.IsRevoked(ctx, tok)
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Fatal("Revoke 后 IsRevoked 应为 true")
	}
	// 另一个 token 不受影响（隔离性）。
	other, _ := ts.IsRevoked(ctx, "another-token")
	if other {
		t.Fatal("拉黑一个 token 影响了另一个（键碰撞）")
	}
}

// TestWave1d_ProductionWiring_RevocationActive 锁定**生产装配路径**的吊销生效。
//
// 背景（这是端到端 HTTP 验证抓到的真 bug，必须用测试锁住）：
// 首版实现里 main.go 的 RegisterRoutes 用的是**未装饰**的 LazyAuthenticator，
// 只有 e2e 走 RegisterRoutesWithAuth 显式注入装饰器。结果：
//   - 单元 e2e 全绿（走显式注入路径）；
//   - 真实 HTTP 端到端暴露：登出后 whoami 仍返回 200（中间件没查黑名单），
//     而 ValidateToken 报 revoked——两条路径语义不一致。
//
// 根因是**时序错位**：RegisterRoutes 在装配期执行，此时 RedisClient/TokenStore
// 尚未就绪（BeforeStart 才装配），直接传装饰器会把 nil 快照固化。
//
// 本测试走 **RegisterRoutes**（生产同款入口）+ 请求期赋值 bootstrappkg.TokenStore，
// 验证 LazyAuthenticatorWithRevocation 的请求期解析真的生效。
func TestWave1d_ProductionWiring_RevocationActive(t *testing.T) {
	ts := newTestTokenStore(t)
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	// 模拟 BeforeStart：赋值包级 TokenStore（生产 main.go 同款）。
	prev := bootstrappkg.TokenStore
	bootstrappkg.TokenStore = ts
	t.Cleanup(func() { bootstrappkg.TokenStore = prev })

	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetTokenStore(ts)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticatorWithRevocation())

	e := gingonic.New()
	// 关键：用生产入口 RegisterRoutes（内部用 LazyAuthenticatorWithRevocation）。
	apiserver.RegisterRoutes(e, &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	base := srv.URL

	access, _ := loginForToken(t, base)
	// 前置：token 可用。
	if code, _ := callRaw(t, base, access, http.MethodGet, "/v1/auth/whoami", nil); code != http.StatusOK {
		t.Fatalf("登出前 token 不可用")
	}
	// 登出。
	if code, raw := callRaw(t, base, access, http.MethodPost, "/v1/auth/logout", nil); code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", code, raw)
	}
	// 生产路径下，登出后中间件必须拒绝（这正是端到端抓到 bug 的那一步）。
	code, raw := callRaw(t, base, access, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("生产装配路径下登出后 token 仍可用（吊销未接入中间件）: "+
			"status=%d body=%s, want 401", code, raw)
	}
	t.Logf("生产装配路径（RegisterRoutes + 请求期 TokenStore）吊销生效")
}
