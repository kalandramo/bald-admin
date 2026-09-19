package e2e

// t_w1d_register_e2e_test.go —— Wave 1d-3：RegisterUser（源 authentication.proto L31）。
//
// 验收：
//   - 注册新用户 → 返回 user_id；该用户**能立即登录**（这是最关键的端到端断言：
//     源项目曾有此 bug——密码哈希对象不一致导致注册用户永远无法登录）；
//   - 重复用户名 → 409 冲突（不覆盖已有用户）；
//   - 弱密码/短用户名 → 400（对齐契约的 min_len 约束）；
//   - 无效租户码 → 400（源项目同款：租户模式下注册必须归属有效租户）。
//
// 契约偏差（记录）：源 RegisterUserResponse.user_id 是 uint32，而 bald-admin 的
// User.ID 是 string（如 "u-admin"）——ID 体系不同。此处 user_id 字段承载字符串 ID
//（HTTP JSON 无类型约束），并额外提供 id 字段。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// TestWave1d_RegisterUser 注册后必须能立即登录——这是最关键的断言。
//
// 用**唯一用户名**（时间戳后缀）：包内其他测试（store_login_e2e_test.go:110）
// 使用固定用户名 "newbie"，共享固定数据会与注册测试互相污染——shuffle 模式下
// 顺序不定，先跑者成功、后跑者撞唯一约束。这是本会话实测到的真实污染
//（`go test -shuffle=on` 时 TestStoreWrite_TenantInjection 因 "newbie" 冲突 FAIL）。
func TestWave1d_RegisterUser(t *testing.T) {
	base := startAuthRESTWithCaptcha(t, nil)

	uname := "reg" + strconv.FormatInt(time.Now().UnixNano()%1000000, 10)
	const pass = "secret12345"
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register",
		map[string]any{
			"username":    uname,
			"password":    pass,
			"tenant_code": "t-default",
		})
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s, want 200/201", code, raw)
	}
	var res struct {
		UserID string `json:"user_id"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if res.UserID == "" && res.ID == "" {
		t.Fatalf("register 未返回 user_id: %s", raw)
	}
	t.Logf("注册成功: user_id=%s", res.UserID)

	// **最关键**：新用户能立即登录（证明密码哈希对象一致）。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": uname, "password": pass})
	if code != http.StatusOK {
		t.Fatalf("注册后无法登录（哈希对象不一致的经典 bug）: status=%d body=%s", code, raw)
	}
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(raw, &pair)
	if pair.AccessToken == "" {
		t.Fatalf("登录未返回 token: %s", raw)
	}
	t.Logf("注册用户可立即登录 ✅（密码哈希闭环正确）")
}

// TestWave1d_RegisterUser_DuplicateUsername 重复用户名必须被拒（不覆盖已有用户）。
func TestWave1d_RegisterUser_DuplicateUsername(t *testing.T) {
	base := startAuthRESTWithCaptcha(t, nil)

	// admin 已存在（种子用户）。
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register",
		map[string]any{
			"username":    "admin",
			"password":    "whatever12345",
			"tenant_code": "t-default",
		})
	if code == http.StatusOK || code == http.StatusCreated {
		t.Fatalf("重复用户名被接受（会覆盖已有用户，安全缺陷）: %s", raw)
	}
	if code != http.StatusConflict {
		t.Fatalf("重复用户名 status=%d, want 409", code)
	}
	// 且 admin 的原密码仍有效（未被覆盖）。
	code, _ = callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": "admin", "password": "admin123"})
	if code != http.StatusOK {
		t.Fatalf("admin 原密码失效——注册覆盖了已有用户: status=%d", code)
	}
	t.Logf("重复用户名被拒且原用户未受影响 ✅")
}

// TestWave1d_RegisterUser_Validation 弱密码/短用户名必须 400。
func TestWave1d_RegisterUser_Validation(t *testing.T) {
	base := startAuthRESTWithCaptcha(t, nil)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"用户名过短", map[string]any{"username": "ab", "password": "secret12345", "tenant_code": "t-default"}},
		{"密码过短", map[string]any{"username": "validuser", "password": "short", "tenant_code": "t-default"}},
		{"空用户名", map[string]any{"username": "", "password": "secret12345", "tenant_code": "t-default"}},
	}
	for _, tc := range cases {
		code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register", tc.body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s, want 400", tc.name, code, raw)
		}
	}
	t.Logf("校验约束生效（用户名 ≥3、密码 ≥8）✅")
}

// TestWave1d_RegisterUser_PolicyReload 锁定「注册后权限立即可用」。
//
// 背景（真实缺陷，端到端 HTTP 抓到）：casbin 的 g 行（subject→角色绑定）在启动时
// 经 loadPolicyCSV 从 UserStore **一次性**装载。新注册用户不在其中——不重载则
// 注册后立即访问受保护资源会 403（实测 subject=u-xxx, object=auth, action=get
// 被拒），须重启进程才生效。
//
// 根因是框架能力缺口：authz.Authorizer 接口只有 Authorize（pkg/authz/authz.go:15），
// contrib/authz-casbin 也无热重载入口。应用层用 ReloadableAuthorizer 装饰器绕行。
//
// 本测试走**生产装配路径**（InitBridges → RegisterRoutes），验证注册后新用户的
// 权限立即生效——若去掉 ReloadPolicies 调用，此测试立即 RED。
func TestWave1d_RegisterUser_PolicyReload(t *testing.T) {
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	base := startAuthRESTWithCaptcha(t, nil)

	uname := "policyreload" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register",
		map[string]any{"username": uname, "password": "secret12345", "tenant_code": "t-default"})
	if code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s, want 201", code, raw)
	}

	// 注册后立即登录。
	code, raw = callRaw(t, base, "", http.MethodPost, "/v1/login",
		map[string]any{"username": uname, "password": "secret12345"})
	if code != http.StatusOK {
		t.Fatalf("登录 status=%d body=%s", code, raw)
	}
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(raw, &pair)

	// **关键断言**：新用户的 token 立即能通过授权（viewer 有 auth:get）。
	code, raw = callRaw(t, base, pair.AccessToken, http.MethodGet, "/v1/auth/whoami", nil)
	if code != http.StatusOK {
		t.Fatalf("注册后立即访问受保护资源失败（策略未热重载）: status=%d body=%s\n"+
			"根因：casbin g 行启动时一次性装载，新用户须重启才生效", code, raw)
	}
	t.Logf("注册后权限立即可用 ✅（策略热重载生效）")
}

// TestWave1d_RegisterUser_InvalidTenant 无效租户码必须 400。
func TestWave1d_RegisterUser_InvalidTenant(t *testing.T) {
	base := startAuthRESTWithCaptcha(t, nil)

	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/register",
		map[string]any{
			"username":    "tenantless",
			"password":    "secret12345",
			"tenant_code": "no-such-tenant",
		})
	if code != http.StatusBadRequest {
		t.Fatalf("无效租户 status=%d body=%s, want 400", code, raw)
	}
	t.Logf("无效租户码被拒 ✅")
}
