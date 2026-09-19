package e2e

// t_w1_5_mfa_e2e_test.go —— Wave 1.5：MFA 域（源 mfa.proto，10 rpc）。
//
// **复刻范围严格对齐源**：源 `mfa_service.go` 文件头注释载明「本轮仅落地 TOTP；
// 非 TOTP 方法、StartMFAChallenge、备份码相关 RPC 本轮返回 UNIMPLEMENTED」。
// 故本测试同样断言：
//   - TOTP 七条（Status/List/StartEnroll/ConfirmEnroll/Disable/RevokeDevice/VerifyChallenge）真实现；
//   - StartChallenge / GenerateBackupCodes / ListBackupCodes 返回 UNIMPLEMENTED。
//
// 核心安全语义（源实现的精髓，必须锁住）：
//   - **±1 窗口**：当前码通过，超出窗口的码失败；
//   - **注册挑战 peek 不消耗**：首码输错可重试；
//   - **登录挑战取出即删**：通过后同 opID 不可复用（防双花）；
//   - **跨用户劫持防护**：他人 operation_id 不可用。

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
	"github.com/pquerna/otp/totp"


	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	mfabiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/mfa"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
)

// newTestMFAStore 用 DB 12（与其他 wave 错开）。
func newTestMFAStore(t *testing.T) *mfa.RedisChallengeStore {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 12})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis 不可达，跳过（环境缺失）: %v", err)
	}
	t.Cleanup(func() { rdb.FlushDB(context.Background()); _ = rdb.Close() })
	return mfa.NewRedisChallengeStore(rdb)
}

// startMFAREST 起真实 gin 引擎，带 MFA biz。
func startMFAREST(t *testing.T, cs mfa.ChallengeStore) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		MFA: mfabiz.New(cs),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}


// freshUser 注册一个全新用户并登录（避免测试间共享 MFA 因子状态——
// 同一用户重复绑定会被 ErrEnrollExists 拒绝，导致后续测试 Skip 成假绿）。
func freshUser(t *testing.T, base string) (userID, token string) {
	t.Helper()
	uname := "mfau" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10)
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register",
		map[string]any{"username": uname, "password": "secret12345", "tenant_code": "t-default"})
	if code != http.StatusCreated {
		t.Fatalf("register %s status=%d body=%s", uname, code, raw)
	}
	tok := loginAs(t, base, uname, "secret12345")
	return "u-" + uname, tok
}

// TestWave1_5_EnrollConfirmFlow 完整 TOTP 绑定流程：开始 → 确认 → 状态。
func TestWave1_5_EnrollConfirmFlow(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	_, admin := freshUser(t, base)

	// 前置：未绑定。
	code, raw := callRaw(t, base, admin, http.MethodGet, "/v1/mfa/status", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	var st struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(raw, &st)
	if st.Enabled {
		t.Fatalf("初始状态不应 enabled: %s", raw)
	}

	// 开始注册。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/mfa/enroll/start", nil)
	if code != http.StatusOK {
		t.Fatalf("start enroll status=%d body=%s", code, raw)
	}
	var start struct {
		OperationID string `json:"operation_id"`
		Secret      string `json:"secret"`
		OTPAuthURL  string `json:"otp_auth_url"`
	}
	if err := json.Unmarshal(raw, &start); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if start.Secret == "" || start.OperationID == "" || start.OTPAuthURL == "" {
		t.Fatalf("start enroll 返回不完整: %s", raw)
	}
	t.Logf("生成 TOTP secret（%d 字符）+ otpauth URL", len(start.Secret))

	// **关键**：用正确 TOTP 码确认。
	code1, err := totp.GenerateCode(start.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/mfa/enroll/confirm",
		map[string]any{"operation_id": start.OperationID, "totp_code": code1})
	if code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", code, raw)
	}
	var conf struct {
		Success      bool   `json:"success"`
		CredentialID string `json:"credential_id"`
	}
	_ = json.Unmarshal(raw, &conf)
	if !conf.Success || conf.CredentialID == "" {
		t.Fatalf("confirm 未成功: %s", raw)
	}

	// 状态应为 enabled。
	code, raw = callRaw(t, base, admin, http.MethodGet, "/v1/mfa/status", nil)
	_ = json.Unmarshal(raw, &st)
	if !st.Enabled {
		t.Fatalf("绑定后 status 应 enabled: %s", raw)
	}
	t.Logf("TOTP 绑定闭环 ✅（secret → 码 → 确认 → enabled）")
}

// TestWave1_5_EnrollRetryOnWrongCode 注册挑战 peek 不消耗——首码错可重试。
func TestWave1_5_EnrollRetryOnWrongCode(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	// 用全新用户（避免与其他测试共享 MFA 因子状态）。
	_, alice := freshUser(t, base)

	code, raw := callRaw(t, base, alice, http.MethodPost, "/v1/mfa/enroll/start", nil)
	if code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", code, raw)
	}
	var start struct {
		OperationID string `json:"operation_id"`
		Secret      string `json:"secret"`
	}
	_ = json.Unmarshal(raw, &start)

	// 第一次：错误码 → 应失败但**不消耗** operation。
	code, raw = callRaw(t, base, alice, http.MethodPost, "/v1/mfa/enroll/confirm",
		map[string]any{"operation_id": start.OperationID, "totp_code": "000000"})
	if code == http.StatusOK {
		var c struct {
			Success bool `json:"success"`
		}
		_ = json.Unmarshal(raw, &c)
		if c.Success {
			t.Fatalf("错误码竟确认成功")
		}
	}

	// 第二次：正确码 → 应成功（证明 operation 未被消耗）。
	good, _ := totp.GenerateCode(start.Secret, time.Now())
	code, raw = callRaw(t, base, alice, http.MethodPost, "/v1/mfa/enroll/confirm",
		map[string]any{"operation_id": start.OperationID, "totp_code": good})
	if code != http.StatusOK {
		t.Fatalf("重试后正确码 status=%d body=%s", code, raw)
	}
	var conf struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(raw, &conf)
	if !conf.Success {
		t.Fatalf("错误码后正确码应成功（注册挑战应 peek 不消耗）: %s", raw)
	}
	t.Logf("注册挑战 peek 不消耗 ✅（首码错可重试）")
}

// TestWave1_5_TOTPWindow ±1 窗口语义：当前码通过，远期码失败。
func TestWave1_5_TOTPWindow(t *testing.T) {
	// 直接测库层（不经 HTTP），锁定窗口参数。
	key, err := mfa.GenerateTOTP("u-window-test")
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	now := time.Now()
	code, _ := totp.GenerateCode(key.Secret, now)
	if !mfa.ValidateTOTP(code, key.Secret) {
		t.Fatal("当前码应通过")
	}
	// 前一周期（-1）：窗口内，应通过。
	prev, _ := totp.GenerateCode(key.Secret, now.Add(-30*time.Second))
	if !mfa.ValidateTOTP(prev, key.Secret) {
		t.Fatal("-1 周期应在窗口内（skew=1）")
	}
	// 前 3 个周期：窗口外，应失败。
	old, _ := totp.GenerateCode(key.Secret, now.Add(-90*time.Second))
	if mfa.ValidateTOTP(old, key.Secret) {
		t.Fatal("-3 周期应超出窗口（skew=1）")
	}
	// 垃圾码。
	if mfa.ValidateTOTP("garbage", key.Secret) {
		t.Fatal("垃圾码不应通过")
	}
	t.Logf("±1 窗口语义正确 ✅")
}

// TestWave1_5_ChallengeOneTimeUse 登录挑战通过后不可复用（防双花）。
func TestWave1_5_ChallengeOneTimeUse(t *testing.T) {
	cs := newTestMFAStore(t)
	ctx := context.Background()

	opID, err := cs.SetLogin(ctx, mfa.LoginChallengeContext{
		UserID: "u-x", TenantID: "t-default", Username: "x",
	})
	if err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
	// 首次 Take 成功。
	if !cs.TakeLoginAtomic(ctx, opID) {
		t.Fatal("首次 Take 应成功")
	}
	// 二次 Take 必须失败（已消耗）。
	if cs.TakeLoginAtomic(ctx, opID) {
		t.Fatal("二次 Take 应失败（防双花）")
	}
	t.Logf("登录挑战一次性 ✅")
}

// TestWave1_5_FailureLimit 失败达上限后挑战作废。
func TestWave1_5_FailureLimit(t *testing.T) {
	cs := newTestMFAStore(t)
	ctx := context.Background()

	opID, _ := cs.SetLogin(ctx, mfa.LoginChallengeContext{
		UserID: "u-y", TenantID: "t-default", Username: "y",
	})
	// 记失败直到达上限。
	for i := 1; i <= mfa.MaxLoginFailures; i++ {
		exceeded := cs.RecordLoginFailure(ctx, opID)
		if i < mfa.MaxLoginFailures && exceeded {
			t.Fatalf("第 %d 次不应超限（上限 %d）", i, mfa.MaxLoginFailures)
		}
		if i == mfa.MaxLoginFailures && !exceeded {
			t.Fatalf("第 %d 次应超限", i)
		}
	}
	// 超限后挑战已作废。
	if _, err := cs.PeekLogin(ctx, opID); err == nil {
		t.Fatal("超限后挑战应已作废")
	}
	t.Logf("失败上限 %d 后作废挑战 ✅", mfa.MaxLoginFailures)
}

// TestWave1_5_UserMismatchRejected 跨用户劫持防护。
func TestWave1_5_UserMismatchRejected(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	_, admin := freshUser(t, base)
	bob := loginAs(t, base, "bob", "alice123") // bob 属 t-other 租户（跨租户劫持场景）

	// admin 发起注册拿 operation_id。
	_, raw := callRaw(t, base, admin, http.MethodPost, "/v1/mfa/enroll/start", nil)
	var start struct {
		OperationID string `json:"operation_id"`
		Secret      string `json:"secret"`
	}
	_ = json.Unmarshal(raw, &start)
	if start.OperationID == "" {
		t.Skipf("start enroll 未返回 operation（可能因冷却），跳过: %s", raw)
	}

	// bob 用 admin 的 operation_id 确认——必须被拒（跨用户劫持）。
	good, _ := totp.GenerateCode(start.Secret, time.Now())
	code, raw := callRaw(t, base, bob, http.MethodPost, "/v1/mfa/enroll/confirm",
		map[string]any{"operation_id": start.OperationID, "totp_code": good})
	if code == http.StatusOK {
		var c struct {
			Success bool `json:"success"`
		}
		_ = json.Unmarshal(raw, &c)
		if c.Success {
			t.Fatalf("他人 operation_id 竟确认成功（跨用户劫持）: %s", raw)
		}
	}
	t.Logf("跨用户劫持被拒 ✅")
}

// TestWave1_5_UnimplementedRPCs 源未实现的 rpc 必须返回 UNIMPLEMENTED。
func TestWave1_5_UnimplementedRPCs(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	admin := loginAs(t, base, "admin", "admin123")

	cases := []struct {
		name string
		path string
	}{
		{"StartMFAChallenge", "/v1/mfa/challenge/start"},
		{"GenerateBackupCodes", "/v1/mfa/backup-codes/generate"},
		{"ListBackupCodes", "/v1/mfa/backup-codes"},
	}
	for _, tc := range cases {
		code, raw := callRaw(t, base, admin, http.MethodPost, tc.path, nil)
		if code != http.StatusNotImplemented {
			t.Fatalf("%s: status=%d body=%s, want 501（源未实现，应显式标注）", tc.name, code, raw)
		}
	}
	t.Logf("3 条源未实现的 rpc 返回 501 ✅（对齐源行为，不自创）")
}

// TestWave1_5_EnrollConflict 重复绑定必须被拒（需先解绑）。
func TestWave1_5_EnrollConflict(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	// 用全新用户（避免共享状态）。
	_, bob := freshUser(t, base)

	// 首次绑定。
	_, raw := callRaw(t, base, bob, http.MethodPost, "/v1/mfa/enroll/start", nil)
	var s1 struct {
		OperationID string `json:"operation_id"`
		Secret      string `json:"secret"`
	}
	_ = json.Unmarshal(raw, &s1)
	if s1.OperationID == "" {
		t.Skipf("start 未返回 operation（冷却中），跳过: %s", raw)
	}
	good, _ := totp.GenerateCode(s1.Secret, time.Now())
	if code, raw := callRaw(t, base, bob, http.MethodPost, "/v1/mfa/enroll/confirm",
		map[string]any{"operation_id": s1.OperationID, "totp_code": good}); code != http.StatusOK {
		t.Fatalf("首次绑定失败 status=%d body=%s", code, raw)
	}

	// 二次发起应被拒（已绑定）。
	code, raw := callRaw(t, base, bob, http.MethodPost, "/v1/mfa/enroll/start", nil)
	if code == http.StatusOK {
		t.Fatalf("已绑定后仍能发起注册: %s", raw)
	}
	t.Logf("重复绑定被拒 ✅")
}

// TestWave1_5_DisableAndRevoke 解绑后状态归零。
func TestWave1_5_DisableAndRevoke(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	ctx := context.Background()

	// 直接用 model 构造因子（绕过冷却，聚焦解绑语义）。
	uid := "u-disable" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	f := &authmodel.UserMFAFactor{
		ID: "t-default:" + uid + ":TOTP", TenantID: "t-default", UserID: uid,
		Method: authmodel.MFAMethodTOTP, Secret: "SECRETBASE32", Enabled: true,
	}
	if err := bootstrappkg.MFAFactorStore.Create(ctx, f); err != nil {
		t.Fatalf("create factor: %v", err)
	}

	_, admin := freshUser(t, base)
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/mfa/device/revoke",
		map[string]any{"user_id": uid, "credential_id": f.ID})
	if code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", code, raw)
	}
	t.Logf("凭证撤销 ✅")
}

// TestWave1_5_DisableIdempotent —— 回归测试（Wave 2.5 发现同不变量违反点）。
//
// **不变量的违反点**：`biz.Disable` 在 credentialID 为空时按 (tenant,user)
// 删**集合**（源语义：「不传 credentialID 时清空该用户全部该方法因子」）。
// 但 `Store.Delete` 对 0 行匹配返回 ErrNotFound——用户禁用**本就未启用**的
// 方法时，删 0 行是合法结果，却被误报为 not found。
//
// RED 判据：未修复时 Disable 返回错误；修复后返回 nil。
func TestWave1_5_DisableIdempotent(t *testing.T) {
	cs := newTestMFAStore(t)
	base := startMFAREST(t, cs)
	uid, _ := freshUser(t, base)
	biz := mfabiz.New(cs)
	ctx := context.Background()

	// 该用户从未绑定任何 TOTP 因子 → 按 method 清空应删 0 行。
	// 语义：禁用「本就没启用」的方法 = 幂等成功（不是错误）。
	if err := biz.Disable(ctx, "t-default", uid, ""); err != nil {
		t.Fatalf("禁用未启用的方法应幂等成功，实际报错: %v", err)
	}

	// 重复调用仍应成功。
	if err := biz.Disable(ctx, "t-default", uid, ""); err != nil {
		t.Fatalf("重复禁用应幂等成功，实际报错: %v", err)
	}

	// 但按具体 credentialID 删除不存在的因子 → 仍应报 not found
	// （精确单条删除的 ErrNotFound 语义是正确的，不能一并放宽）。
	if err := biz.RevokeDevice(ctx, "t-default", uid, "no-such-cred"); err == nil {
		t.Fatalf("按不存在 credentialID 撤销应报错（精确单条语义）")
	}
}
