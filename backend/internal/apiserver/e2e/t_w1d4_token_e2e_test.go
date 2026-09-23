package e2e

// t_w1d4_token_e2e_test.go —— Wave 1d-4：token 管理 4 个 rpc
//（源 authentication.proto L41/L44/L47/L50）。
//
// 验收：
//   - GetAccessTokens：列出某用户的活跃访问令牌；
//   - BlockToken：拉黑指定 token，**拉黑后该 token 立即被认证中间件拒绝**；
//   - UnblockToken：解除拉黑，token 恢复可用；
//   - RevokeTokenById：按标识撤销指定 token（与 BlockToken 同源，语义稍异）；
//   - 权限：非管理员不得操作他人 token。
//
// 契约偏差（显式记录）：
//   - 源 `user_id` 是 uint32，bald-admin 的 User.ID 是 string → 同 RegisterUser，
//     字段承载字符串 ID。
//   - 源以 `jti` 为核心标识，但 bald 的 JWT **不签发 jti**（框架缺陷 D6，
//     `bald/contrib/authn-jwt/jwt.go:208-221` 的 toJWT 不设 RegisteredClaims.ID）。
//     本实现以 **token 指纹**（SHA-256）作为 jti 的等价物——GetAccessTokens 返回
//     的是 token 明文，客户端回传时两种字段（token/jti）都按指纹匹配。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	"github.com/kalandramo/bald-admin/internal/security/token"
)

// startTokenMgmtREST 起真实 gin 引擎，带 token 存储 + 吊销检查（生产同款装配）。
func startTokenMgmtREST(t *testing.T, ts token.Store) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetTokenStore(ts)

	var authenticator authn.Authenticator = bootstrappkg.LazyAuthenticator()
	if ts != nil {
		authenticator = token.NewRevocationChecker(authenticator, ts)
	}
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

// newTestTokenStoreDB13 用 DB 16（与 Wave 1d-1 的 DB 15 隔离，避免测试间干扰）。
func newTestTokenStoreDB13(t *testing.T) *token.RedisStore {
	t.Helper()
	rdb := goredis.NewClient(redisTestOptions(13))
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis 不可达，跳过（环境缺失）: %v", err)
	}
	t.Cleanup(func() { rdb.FlushDB(context.Background()); _ = rdb.Close() })
	return token.NewRedisStore(rdb)
}

// loginAs 登录并返回 access_token。
func loginAs(t *testing.T, base, user, pass string) string {
	t.Helper()
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": user, "password": pass})
	if code != http.StatusOK {
		t.Fatalf("login(%s) status=%d body=%s", user, code, raw)
	}
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(raw, &pair)
	return pair.AccessToken
}

// TestWave1d4_GetAccessTokens 列出用户活跃 token。
func TestWave1d4_GetAccessTokens(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")

	// 用独立存储直接登记两个 token（模拟该用户有多个活跃会话）。
	ctx := context.Background()
	uid := "u-toklist" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	tokA, tokB := "tokA-"+uid, "tokB-"+uid
	if err := ts.TrackAccess(ctx, uid, tokA, time.Hour); err != nil {
		t.Fatalf("TrackAccess A: %v", err)
	}
	if err := ts.TrackAccess(ctx, uid, tokB, time.Hour); err != nil {
		t.Fatalf("TrackAccess B: %v", err)
	}

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens",
		map[string]any{"user_id": uid})
	if code != http.StatusOK {
		t.Fatalf("getAccessTokens status=%d body=%s", code, raw)
	}
	var res struct {
		AccessTokens []string `json:"access_tokens"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if len(res.AccessTokens) != 2 {
		t.Fatalf("期望 2 个活跃 token，实得 %d: %v", len(res.AccessTokens), res.AccessTokens)
	}
	found := map[string]bool{}
	for _, tk := range res.AccessTokens {
		found[tk] = true
	}
	if !found[tokA] || !found[tokB] {
		t.Fatalf("列表未含全部 token: %v", res.AccessTokens)
	}
	t.Logf("GetAccessTokens 返回 2 个活跃 token ✅")
}

// TestWave1d4_BlockToken_RevokesImmediately BlockToken 后 token 必须立即失效。
func TestWave1d4_BlockToken_RevokesImmediately(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")

	// alice 登录拿 token。
	victim := loginAs(t, base, "alice", "alice123")
	// 前置：可用。
	if code, _ := callRaw(t, base, victim, http.MethodGet, "/v1/auth/whoami", nil); code != http.StatusOK {
		t.Fatalf("封禁前 token 不可用")
	}

	// 管理员拉黑该 token。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens/block",
		map[string]any{
			"user_id": "u-alice",
			"token":   victim,
			"reason":  "suspicious activity",
		})
	if code != http.StatusOK {
		t.Fatalf("block status=%d body=%s", code, raw)
	}

	// **关键断言**：被拉黑的 token 立即被中间件拒绝。
	code, raw = callRaw(t, base, victim, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("拉黑后 token 仍可用（黑名单未生效）: status=%d body=%s, want 401", code, raw)
	}
	t.Logf("BlockToken 后 token 立即失效 ✅")
}

// TestWave1d4_UnblockToken 解除拉黑后 token 恢复可用。
func TestWave1d4_UnblockToken(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")
	victim := loginAs(t, base, "alice", "alice123")

	// 拉黑。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens/block",
		map[string]any{"user_id": "u-alice", "token": victim, "reason": "test"}); code != http.StatusOK {
		t.Fatalf("block status=%d body=%s", code, raw)
	}
	if code, _ := callRaw(t, base, victim, http.MethodGet, "/v1/auth/whoami", nil); code != http.StatusUnauthorized {
		t.Fatalf("拉黑后应 401")
	}

	// 解除拉黑。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens/unblock",
		map[string]any{"user_id": "u-alice", "token": victim})
	if code != http.StatusOK {
		t.Fatalf("unblock status=%d body=%s", code, raw)
	}

	// **关键断言**：恢复可用。
	code, raw = callRaw(t, base, victim, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusOK {
		t.Fatalf("解除拉黑后 token 仍不可用: status=%d body=%s, want 200", code, raw)
	}
	t.Logf("UnblockToken 后 token 恢复可用 ✅")
}

// TestWave1d4_RevokeTokenById 按标识撤销。
func TestWave1d4_RevokeTokenById(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")
	victim := loginAs(t, base, "alice", "alice123")

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens/revoke",
		map[string]any{"jti": victim, "reason": "admin revoke", "user_id": "u-alice"})
	if code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", code, raw)
	}
	code, raw = callRaw(t, base, victim, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("撤销后 token 仍可用: status=%d body=%s, want 401", code, raw)
	}
	t.Logf("RevokeTokenById 生效 ✅")
}

// TestWave1d4_RequiresAdmin 非管理员不得操作 token（安全边界）。
func TestWave1d4_RequiresAdmin(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	// alice 是 viewer，无 token 管理权限。
	alice := loginAs(t, base, "alice", "alice123")

	code, raw := callRaw(t, base, alice, http.MethodPost, "/v1/auth/tokens",
		map[string]any{"user_id": "u-admin"})
	if code == http.StatusOK {
		t.Fatalf("viewer 能列出他人 token（越权）: %s", raw)
	}
	if code != http.StatusForbidden {
		t.Fatalf("viewer 列 token status=%d, want 403", code)
	}
	t.Logf("非管理员被拒 ✅")
}

// TestWave1d4_TrackAndList_TTLExpiry 过期的 token 不出现在列表中（ZSET score 语义）。
func TestWave1d4_TrackAndList_TTLExpiry(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	ctx := context.Background()
	uid := "u-ttl" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)

	// 一个已过期（ttl 极短，等一下），一个长期有效。
	if err := ts.TrackAccess(ctx, uid, "shortlived", 50*time.Millisecond); err != nil {
		t.Fatalf("TrackAccess: %v", err)
	}
	if err := ts.TrackAccess(ctx, uid, "longlived", time.Hour); err != nil {
		t.Fatalf("TrackAccess: %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	list, err := ts.ListAccess(ctx, uid)
	if err != nil {
		t.Fatalf("ListAccess: %v", err)
	}
	for _, tk := range list {
		if tk == "shortlived" {
			t.Fatalf("已过期 token 仍出现在列表中: %v", list)
		}
	}
	if len(list) != 1 || list[0] != "longlived" {
		t.Fatalf("期望只剩 longlived，实得 %v", list)
	}
	t.Logf("过期 token 自动剔除 ✅（ZSET score 语义）")
}

// TestWave1d4_LoginTracksToken 端到端闭环：真实登录后，token 必须出现在
// 该用户的活跃列表里（证明 Login 的 TrackAccess 接线生效）。
func TestWave1d4_LoginTracksToken(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")
	alice := loginAs(t, base, "alice", "alice123")

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens",
		map[string]any{"user_id": "u-alice"})
	if code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", code, raw)
	}
	var res struct {
		AccessTokens []string `json:"access_tokens"`
	}
	_ = json.Unmarshal(raw, &res)
	found := false
	for _, tk := range res.AccessTokens {
		if tk == alice {
			found = true
		}
	}
	if !found {
		t.Fatalf("登录后的 token 未出现在活跃列表: %v", res.AccessTokens)
	}
	t.Logf("登录 → 活跃列表闭环 ✅")
}

// TestWave1d4_BlockThenListExcludes 被封禁的 token 不再出现在活跃列表。
func TestWave1d4_BlockThenListExcludes(t *testing.T) {
	ts := newTestTokenStoreDB13(t)
	base := startTokenMgmtREST(t, ts)
	admin := loginAs(t, base, "admin", "admin123")
	victim := loginAs(t, base, "alice", "alice123")

	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens/block",
		map[string]any{"user_id": "u-alice", "token": victim, "reason": "t"}); code != http.StatusOK {
		t.Fatalf("block status=%d body=%s", code, raw)
	}
	_, raw := callRaw(t, base, admin, http.MethodPost, "/v1/auth/tokens",
		map[string]any{"user_id": "u-alice"})
	var res struct {
		AccessTokens []string `json:"access_tokens"`
	}
	_ = json.Unmarshal(raw, &res)
	for _, tk := range res.AccessTokens {
		if tk == victim {
			t.Fatalf("被封禁的 token 仍出现在活跃列表: %v", res.AccessTokens)
		}
	}
	t.Logf("封禁后从活跃列表移除 ✅")
}
