package e2e

// t_w5_3_language_e2e_test.go —— Wave 5.3：语言管理域（源 7 rpc）。
//
// 源 `dict/service/v1/language.proto` 7 rpc：List/Count/Get/Create/
// BatchCreate/Update/Delete。复刻范围严格对齐源：
//   - 6 条真实现（List/Count/Get/Create/Update/Delete）；
//   - BatchCreate **源 service 层未实现**（proto 有、language_service.go 无该方法）
//     → 本实现同样返回 UNIMPLEMENTED（对齐源，不自创）。
//
// 验收点：
//  1. 种子语言 7 条（对齐源 constants.DefaultLanguages），sort_order 升序；
//  2. CRUD 全链路（创建→重复 409→更新→删除→404）；
//  3. Count 与 List 一致；
//  4. BatchCreate → 501（对齐源未实现）；
//  5. 授权：viewer 读 200 / 写 403（language 策略：admin 写、viewer 读）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// startLanguageREST 起真实 gin 引擎 + 真实 SQLite（语言 biz 注入）。
func startLanguageREST(t *testing.T) string {
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
		Language: langbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestWave5_3_SeedAndList 种子语言 + List 排序（对齐源 7 条默认语言）。
func TestWave5_3_SeedAndList(t *testing.T) {
	base := startLanguageREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/v1/language", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", code, raw)
	}
	out := new(dictv1.ListLanguagesResponse)
	decodePB(raw, out)
	if out.GetTotal() != 7 {
		t.Fatalf("种子语言 total=%d, want 7", out.GetTotal())
	}
	// sort_order 升序：zh-CN(0) → en-US(1) → 其余(100)。
	if out.GetItems()[0].GetId() != "zh-CN" {
		t.Fatalf("首个语言=%s, want zh-CN (sort_order=0)", out.GetItems()[0].GetId())
	}
	if !out.GetItems()[0].GetIsDefault() {
		t.Fatalf("zh-CN 应为默认语言")
	}
	if out.GetItems()[1].GetId() != "en-US" {
		t.Fatalf("第二个语言=%s, want en-US (sort_order=1)", out.GetItems()[1].GetId())
	}
	t.Logf("种子语言正确：total=%d 首个=%s(默认) 次个=%s",
		out.GetTotal(), out.GetItems()[0].GetId(), out.GetItems()[1].GetId())
}

// TestWave5_3_Count 统计数量（与 List total 一致）。
func TestWave5_3_Count(t *testing.T) {
	base := startLanguageREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	code, raw := callRaw(t, base, tok, http.MethodGet, "/v1/language/count", nil)
	if code != http.StatusOK {
		t.Fatalf("count status=%d body=%s", code, raw)
	}
	out := new(dictv1.CountLanguagesResponse)
	decodePB(raw, out)
	if out.GetCount() != 7 {
		t.Fatalf("count=%d, want 7", out.GetCount())
	}
}

// TestWave5_3_CRUD 创建→重复 409→更新→删除→404。
func TestWave5_3_CRUD(t *testing.T) {
	base := startLanguageREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	// 创建。
	body := map[string]any{"id": "de-DE", "language_name": "德语", "native_name": "Deutsch",
		"is_enabled": true, "sort_order": 200}
	if code, raw := callRaw(t, base, tok, http.MethodPost, "/v1/language", body); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}
	// 重复 → 409。
	if code, _ := callRaw(t, base, tok, http.MethodPost, "/v1/language", body); code != http.StatusConflict {
		t.Fatalf("duplicate create must 409, got %d", code)
	}
	// 取单个。
	code, raw := callRaw(t, base, tok, http.MethodGet, "/v1/language/de-DE", nil)
	if code != http.StatusOK {
		t.Fatalf("get status=%d", code)
	}
	got := new(dictv1.GetLanguageResponse)
	decodePB(raw, got)
	if got.GetLanguage().GetLanguageName() != "德语" {
		t.Fatalf("get language_name=%q", got.GetLanguage().GetLanguageName())
	}
	// 更新（改名 + 显式设默认）。
	upd := map[string]any{"language_name": "德语(改)", "is_default": true, "is_default_set": true}
	if code, raw := callRaw(t, base, tok, http.MethodPut, "/v1/language/de-DE", upd); code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", code, raw)
	}
	code, raw = callRaw(t, base, tok, http.MethodGet, "/v1/language/de-DE", nil)
	if code != http.StatusOK {
		t.Fatalf("get after update status=%d", code)
	}
	got = new(dictv1.GetLanguageResponse)
	decodePB(raw, got)
	if got.GetLanguage().GetLanguageName() != "德语(改)" || !got.GetLanguage().GetIsDefault() {
		t.Fatalf("update 未生效: %+v", got.GetLanguage())
	}
	// 删除 → 200，再取 → 404。
	if code, _ := callRaw(t, base, tok, http.MethodDelete, "/v1/language/de-DE", nil); code != http.StatusOK {
		t.Fatalf("delete status=%d", code)
	}
	if code, _ := callRaw(t, base, tok, http.MethodGet, "/v1/language/de-DE", nil); code != http.StatusNotFound {
		t.Fatalf("get after delete must 404, got %d", code)
	}
}

// TestWave5_3_BatchCreateUnimplemented BatchCreate → 501（对齐源未实现）。
func TestWave5_3_BatchCreateUnimplemented(t *testing.T) {
	base := startLanguageREST(t)
	tok := tenantToken(t, "admin", "u-admin", "admin", "t-default")

	body := map[string]any{"items": []map[string]any{
		{"id": "it-IT", "language_name": "意大利语", "native_name": "Italiano"},
	}}
	code, raw := callRaw(t, base, tok, http.MethodPost, "/v1/language/batch", body)
	if code != http.StatusNotImplemented {
		t.Fatalf("batch create 应 501（源未实现），got %d body=%s", code, raw)
	}
	t.Logf("BatchCreate 返回 501（对齐源未实现）")
}

// TestWave5_3_ViewerPermission 授权：viewer 读 200 / 写 403。
func TestWave5_3_ViewerPermission(t *testing.T) {
	base := startLanguageREST(t)
	viewer := tenantToken(t, "alice", "u-alice", "viewer", "t-default")

	if code, _ := callRaw(t, base, viewer, http.MethodGet, "/v1/language", nil); code != http.StatusOK {
		t.Fatalf("viewer list want 200, got %d", code)
	}
	if code, _ := callRaw(t, base, viewer, http.MethodPost, "/v1/language",
		map[string]any{"id": "x", "language_name": "x"}); code != http.StatusForbidden {
		t.Fatalf("viewer create want 403, got %d", code)
	}
	if code, _ := callRaw(t, base, viewer, http.MethodDelete, "/v1/language/zh-CN", nil); code != http.StatusForbidden {
		t.Fatalf("viewer delete want 403, got %d", code)
	}
}
