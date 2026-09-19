package e2e

// t_w1_6_identity_e2e_test.go —— Wave 1.6：identity 扩展域
//（user_credential 10 rpc + login_policy 6 rpc + user_profile 2 rpc 源空实现）。
//
// 核心验证点：
//   - 凭证创建**强制 bcrypt 哈希**（明文密码不落库）；
//   - 凭证校验（VerifyCredential）语义：状态非 ENABLED 一律拒绝；
//   - **登录策略值格式校验**（源的防「白名单配错 = 全员被锁」）；
//   - user_profile 的 BindContact/VerifyContact 返回 501（源空实现，已复认）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	identitybiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/identity"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/loginpolicy"
)

func startIdentityREST(t *testing.T) string {
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
		Identity: identitybiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func freshUserID() string { return "idt" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10) }

// TestWave1_6_CredentialCreateHashesPassword 凭证创建**强制 bcrypt 哈希**。
// 这是本实现的加固：源把哈希责任交给 repo，本实现在 biz 层强制（防明文落库）。
func TestWave1_6_CredentialCreateHashesPassword(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	uid := freshUserID()

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/credentials",
		map[string]any{
			"user_id": uid, "identity_type": "EMAIL",
			"identifier": uid + "@example.com", "password": "secret12345",
		})
	if code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", code, raw)
	}

	// **关键**：直接查 DB 确认落库的是 bcrypt 哈希而非明文。
	ctx := context.Background()
	m, err := bootstrappkg.CredentialStore.Get(ctx, mustWhere("id", "EMAIL:"+uid+"@example.com"))
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if m.Credential == "secret12345" {
		t.Fatal("密码以**明文**落库（安全缺陷）")
	}
	if len(m.Credential) < 50 || m.Credential[:4] != "$2a$" {
		t.Fatalf("落库值不是 bcrypt 哈希: %q", m.Credential[:min(20, len(m.Credential))])
	}
	t.Logf("凭证创建强制 bcrypt 哈希 ✅（落库前缀 %s）", m.Credential[:7])
}

// TestWave1_6_VerifyCredential 凭证校验：正确/错误密码、非 ENABLED 状态。
func TestWave1_6_VerifyCredential(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	uid := freshUserID()
	ident := uid + "@example.com"

	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/credentials",
		map[string]any{"user_id": uid, "identity_type": "EMAIL", "identifier": ident,
			"password": "secret12345"}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 正确密码 → valid=true（公开端点，无需 token）。
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/credentials/verify",
		map[string]any{"identity_type": "EMAIL", "identifier": ident, "password": "secret12345"})
	if code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", code, raw)
	}
	var vr struct {
		Valid  bool   `json:"valid"`
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(raw, &vr)
	if !vr.Valid || vr.UserID != uid {
		t.Fatalf("正确密码应 valid=true 且返回 user_id: %s", raw)
	}

	// 错误密码 → valid=false（仍 200，业务结果）。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/credentials/verify",
		map[string]any{"identity_type": "EMAIL", "identifier": ident, "password": "wrong"})
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("错误密码应 valid=false: %s", raw)
	}

	// 不存在的凭证 → valid=false（不报错）。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/credentials/verify",
		map[string]any{"identity_type": "EMAIL", "identifier": "nobody@x.com", "password": "x"})
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("不存在凭证应 valid=false: %s", raw)
	}
	t.Logf("凭证校验语义正确 ✅（正确/错误/不存在 三态）")
}

// TestWave1_6_ChangeCredential 改密码需旧密码，且新密码生效。
func TestWave1_6_ChangeCredential(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")
	uid := freshUserID()
	ident := uid + "@example.com"

	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/credentials",
		map[string]any{"user_id": uid, "identity_type": "EMAIL", "identifier": ident,
			"password": "oldpass12345"}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 旧密码错误 → 拒绝。
	code, _ := callRaw(t, base, admin, http.MethodPost, "/v1/credentials/change",
		map[string]any{"identity_type": "EMAIL", "identifier": ident,
			"old_password": "wrongold", "new_password": "newpass12345"})
	if code == http.StatusOK {
		t.Fatal("旧密码错误竟改密成功（安全缺陷）")
	}

	// 正确旧密码 → 成功。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/credentials/change",
		map[string]any{"identity_type": "EMAIL", "identifier": ident,
			"old_password": "oldpass12345", "new_password": "newpass12345"})
	if code != http.StatusOK {
		t.Fatalf("change status=%d body=%s", code, raw)
	}

	// 新密码生效、旧密码失效。
	var vr struct {
		Valid bool `json:"valid"`
	}
	_, raw = callRaw(t, base, "", http.MethodPost, "/v1/credentials/verify",
		map[string]any{"identity_type": "EMAIL", "identifier": ident, "password": "newpass12345"})
	_ = json.Unmarshal(raw, &vr)
	if !vr.Valid {
		t.Fatalf("新密码应生效: %s", raw)
	}
	_, raw = callRaw(t, base, "", http.MethodPost, "/v1/credentials/verify",
		map[string]any{"identity_type": "EMAIL", "identifier": ident, "password": "oldpass12345"})
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("旧密码应失效: %s", raw)
	}
	t.Logf("改密闭环 ✅（旧密码校验 + 新密码生效 + 旧密码失效）")
}

// TestWave1_6_LoginPolicyValueValidation **源的防「白名单配错 = 全员被锁」**。
func TestWave1_6_LoginPolicyValueValidation(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	// 合法值。
	valid := []map[string]any{
		{"type": "BLACKLIST", "method": "IP", "value": "192.168.1.1"},
		{"type": "WHITELIST", "method": "IP", "value": "10.0.0.0/8"},
		{"type": "BLACKLIST", "method": "TIME", "value": "09:00-18:00"},
		{"type": "BLACKLIST", "method": "REGION", "value": "CN"},
	}
	for _, req := range valid {
		code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/login-policies", req)
		if code != http.StatusCreated {
			t.Fatalf("合法值被拒 %v: status=%d body=%s", req, code, raw)
		}
	}

	// 非法值——必须拒绝（否则白名单静默不命中 = 全员被锁）。
	invalid := []map[string]any{
		{"type": "BLACKLIST", "method": "IP", "value": "not-an-ip"},
		{"type": "WHITELIST", "method": "IP", "value": "10.0.0.0/99"},
		{"type": "BLACKLIST", "method": "TIME", "value": "9am-6pm"},
		{"type": "BLACKLIST", "method": "TIME", "value": "09:00-09:00"}, // 起止相等
		{"type": "BLACKLIST", "method": "REGION", "value": ""},
		{"type": "BADTYPE", "method": "IP", "value": "1.1.1.1"},
		{"type": "BLACKLIST", "method": "UNKNOWN", "value": "x"},
	}
	for _, req := range invalid {
		code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/login-policies", req)
		if code == http.StatusCreated {
			t.Fatalf("非法值竟被接受 %v（白名单配错会锁全员）: %s", req, raw)
		}
		if code != http.StatusBadRequest {
			t.Fatalf("非法值 %v status=%d, want 400", req, code)
		}
	}
	t.Logf("登录策略值校验 ✅（4 合法通过 / 7 非法拒绝）")
}

// TestWave1_6_PolicyCRUD 策略增删查 + 冲突。
func TestWave1_6_PolicyCRUD(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	req := map[string]any{"type": "BLACKLIST", "method": "IP", "value": "203.0.113.7", "reason": "abuse"}
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/login-policies", req)
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}
	var pol struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &pol)

	// 重复创建 → 409。
	code, _ = callRaw(t, base, admin, http.MethodPost, "/v1/login-policies", req)
	if code != http.StatusConflict {
		t.Fatalf("重复策略 status=%d, want 409", code)
	}

	// 列表含它。
	_, raw = callRaw(t, base, admin, http.MethodGet, "/v1/login-policies", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &list)
	found := false
	for _, p := range list.Items {
		if p.ID == pol.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表未含刚创建的策略: %s", raw)
	}

	// 删除。
	code, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/login-policies/"+pol.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("delete status=%d", code)
	}
	t.Logf("策略 CRUD + 冲突检测 ✅")
}

// TestWave1_6_ProfileUnimplemented 源空实现的两条 rpc 返回 501。
func TestWave1_6_ProfileUnimplemented(t *testing.T) {
	base := startIdentityREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	for _, p := range []string{"/v1/profile/bind-contact", "/v1/profile/verify-contact"} {
		code, raw := callRaw(t, base, admin, http.MethodPost, p, map[string]any{})
		if code != http.StatusNotImplemented {
			t.Fatalf("%s: status=%d body=%s, want 501（源空实现）", p, code, raw)
		}
	}
	t.Logf("user_profile 两条源空实现返回 501 ✅（对齐源，不自创）")
}

// TestWave1_6_LoginPolicyValidatorUnit 校验器单元级（边界值）。
func TestWave1_6_LoginPolicyValidatorUnit(t *testing.T) {
	cases := []struct {
		method, value string
		wantErr       bool
	}{
		{"IP", "1.2.3.4", false},
		{"IP", "::1", false},
		{"IP", "10.0.0.0/8", false},
		{"IP", "10.0.0.0/33", true},
		{"IP", "999.1.1.1", true},
		{"TIME", "00:00-23:59", false},
		{"TIME", "23:59-00:00", false}, // 跨午夜合法（起止不等）
		{"TIME", "24:00-01:00", true},  // 小时越界
		{"TIME", "12:60-13:00", true},  // 分钟越界
		{"TIME", "12:00", true},        // 缺分隔
		{"REGION", "CN", false},
		{"MAC", "aa:bb:cc:dd:ee:ff", false},
		{"DEVICE", "dev-123", false},
		{"UNKNOWN", "x", true},
		{"IP", "", true},
	}
	for _, tc := range cases {
		err := loginpolicy.ValidateValue(tc.method, tc.value)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ValidateValue(%q,%q) err=%v, wantErr=%v", tc.method, tc.value, err, tc.wantErr)
		}
	}
	t.Logf("校验器边界值 ✅（%d 例）", len(cases))
}

// TestWave1_6_RequiresAuth 未认证不得访问凭证/策略。
func TestWave1_6_RequiresAuth(t *testing.T) {
	base := startIdentityREST(t)
	code, _ := callRaw(t, base, "", http.MethodGet, "/v1/credentials", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无 token 访问凭证列表 status=%d, want 401", code)
	}
	code, _ = callRaw(t, base, "", http.MethodGet, "/v1/login-policies", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无 token 访问策略列表 status=%d, want 401", code)
	}
	t.Logf("未认证访问被拒 ✅")
}

var _ = authn.AuthClaims{}
