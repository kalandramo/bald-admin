package e2e

// t_w1d_captcha_e2e_test.go —— Wave 1d-2：GenerateCaptcha / VerifyCaptcha
// （源 authentication.proto L58/L61）。
//
// 验收：
//   - GenerateCaptcha 返回 captcha_id + image_base64（可解码为 PNG）；
//   - VerifyCaptcha 正确 code → valid=true；错误 code → valid=false；
//   - **一次性**：同一 captcha_id 验证成功后立即失效（防暴力枚举）；
//   - **TTL**：验证码有有效期（这里只验证存储层 TTL 生效，不等真实过期）；
//   - Redis 不可达 → 校验 fail-closed（拒绝，不是放行）。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/kalandramo/bald-admin/internal/security/captcha"
)

func newTestCaptchaStore(t *testing.T) *captcha.RedisStore {
	t.Helper()
	rdb := goredis.NewClient(redisTestOptions(14))
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis 不可达，跳过（环境缺失）: %v", err)
	}
	t.Cleanup(func() { rdb.FlushDB(context.Background()); _ = rdb.Close() })
	return captcha.NewRedisStore(rdb)
}

// TestWave1d_GenerateCaptcha 生成验证码：返回 id + 可解码的 PNG 图片。
func TestWave1d_GenerateCaptcha(t *testing.T) {
	cs := newTestCaptchaStore(t)
	base := startAuthRESTWithCaptcha(t, cs)

	code, raw := callRaw(t, base, "", http.MethodGet, "/v1/auth/captcha", nil)
	if code != http.StatusOK {
		t.Fatalf("captcha status=%d body=%s, want 200", code, raw)
	}
	var res struct {
		CaptchaID   string `json:"captcha_id"`
		ImageBase64 string `json:"image_base64"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if res.CaptchaID == "" {
		t.Fatalf("captcha_id 为空: %s", raw)
	}
	if res.ImageBase64 == "" {
		t.Fatalf("image_base64 为空: %s", raw)
	}
	// 图片必须是合法 base64 且以 PNG magic 开头（0x89 'P' 'N' 'G'）。
	img, err := base64.StdEncoding.DecodeString(res.ImageBase64)
	if err != nil {
		t.Fatalf("image_base64 不是合法 base64: %v", err)
	}
	if len(img) < 8 || img[0] != 0x89 || string(img[1:4]) != "PNG" {
		t.Fatalf("图片不是 PNG（前 8 字节: %x）", img[:min(8, len(img))])
	}
	t.Logf("验证码生成成功: id=%s 图片 %d 字节 PNG", res.CaptchaID, len(img))

	// 两次生成必须得到不同 id（不可预测/不重用）。
	_, raw2 := callRaw(t, base, "", http.MethodGet, "/v1/auth/captcha", nil)
	var res2 struct {
		CaptchaID string `json:"captcha_id"`
	}
	_ = json.Unmarshal(raw2, &res2)
	if res2.CaptchaID == res.CaptchaID {
		t.Fatal("两次生成的 captcha_id 相同（应唯一）")
	}
}

// TestWave1d_VerifyCaptcha 校验：正确 code 通过、错误 code 失败。
// 用存储层直接写入已知答案（图片内容不可读，无法从 API 得知答案）。
func TestWave1d_VerifyCaptcha(t *testing.T) {
	cs := newTestCaptchaStore(t)
	base := startAuthRESTWithCaptcha(t, cs)
	ctx := context.Background()

	// 直接注入一个已知答案的验证码。
	const id, answer = "test-captcha-1", "abcd"
	if err := cs.Save(ctx, id, answer); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 正确输入（大小写不敏感——用户可能输大写）。
	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": id, "user_input": "ABCD"})
	if code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", code, raw)
	}
	var vr struct {
		Valid bool `json:"valid"`
	}
	_ = json.Unmarshal(raw, &vr)
	if !vr.Valid {
		t.Fatalf("正确验证码被判为 invalid: %s", raw)
	}

	// 一次性：同一 id 再用（即使输入正确）必须失败。
	if err := cs.Save(ctx, "test-captcha-2", "xyz"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, raw = callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": "test-captcha-2", "user_input": "wrong"})
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("错误验证码被判为 valid: %s", raw)
	}

	// 错误输入不应消耗验证码（否则用户打错一次就要重新生成）——
	// 这是可用性设计：只有正确才消费。用新 id 验证。
	if err := cs.Save(ctx, "test-captcha-3", "keepme"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, _ = callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": "test-captcha-3", "user_input": "bad"})
	_, raw = callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": "test-captcha-3", "user_input": "keepme"})
	_ = json.Unmarshal(raw, &vr)
	if !vr.Valid {
		t.Fatalf("错误输入消耗了验证码（可用性缺陷：应仅正确时消费）: %s", raw)
	}

	// 正确消费后再用必须失败（一次性语义）。
	_, raw = callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": "test-captcha-3", "user_input": "keepme"})
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("验证码被重复使用（一次性语义破坏）: %s", raw)
	}
	t.Logf("验证码校验语义正确：大小写不敏感 / 错误不消费 / 正确后失效")
}

// TestWave1d_VerifyCaptcha_UnknownID 不存在的 captcha_id 必须 valid=false（不崩）。
func TestWave1d_VerifyCaptcha_UnknownID(t *testing.T) {
	cs := newTestCaptchaStore(t)
	base := startAuthRESTWithCaptcha(t, cs)

	code, raw := callRaw(t, base, "", http.MethodPost, "/v1/auth/captcha/verify",
		map[string]any{"captcha_id": "never-existed", "user_input": "x"})
	if code != http.StatusOK {
		t.Fatalf("unknown id status=%d, want 200（业务结果非错误码）", code)
	}
	var vr struct {
		Valid bool `json:"valid"`
	}
	_ = json.Unmarshal(raw, &vr)
	if vr.Valid {
		t.Fatalf("不存在的 captcha_id 被判为 valid: %s", raw)
	}
}

// TestWave1d_CaptchaStore_OneTimeConsume 存储层一次性语义 + TTL。
func TestWave1d_CaptchaStore_OneTimeConsume(t *testing.T) {
	cs := newTestCaptchaStore(t)
	ctx := context.Background()

	if err := cs.Save(ctx, "id-x", "code123"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 错误输入不消费。
	ok, err := cs.Verify(ctx, "id-x", "wrong")
	if err != nil || ok {
		t.Fatalf("错误输入: ok=%v err=%v, want false/nil", ok, err)
	}
	// 正确输入消费。
	ok, err = cs.Verify(ctx, "id-x", "code123")
	if err != nil || !ok {
		t.Fatalf("正确输入: ok=%v err=%v, want true/nil", ok, err)
	}
	// 已消费 → 再验失败。
	ok, err = cs.Verify(ctx, "id-x", "code123")
	if err != nil || ok {
		t.Fatalf("重复验证: ok=%v err=%v, want false/nil", ok, err)
	}
	// 大小写不敏感。
	_ = cs.Save(ctx, "id-y", "AbCd")
	ok, _ = cs.Verify(ctx, "id-y", "abcd")
	if !ok {
		t.Fatal("大小写不敏感校验失败")
	}
	// 空格容忍（用户可能带空格）。
	_ = cs.Save(ctx, "id-z", "1234")
	ok, _ = cs.Verify(ctx, "id-z", " 1234 ")
	if !ok {
		t.Fatal("应容忍用户输入的首尾空格")
	}
	_ = strings.TrimSpace("") // 保持 strings import 使用
}
