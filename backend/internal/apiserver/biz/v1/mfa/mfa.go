// Package mfa 实现 MFA 业务逻辑（Wave 1.5，biz 层）。
//
// 复刻范围严格对齐源 `mfa_service.go`：实现 TOTP 的 7 条 rpc；
// StartMFAChallenge / GenerateBackupCodes / ListBackupCodes 返回 ErrUnimplemented
// （源明确未实现——不自创 SMS/WebAuthn/备份码）。
package mfa

import (
	"context"
	"errors"
	"fmt"
	"time"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/mfa"
)

// ErrUnimplemented 该能力源项目未实现（对齐源行为，不自创）。
var ErrUnimplemented = errors.New("mfa: not implemented in source project")

// ErrTooFrequent 注册发起过频（冷却中，handler 归 429 而非 500）。
var ErrTooFrequent = errors.New("mfa: enroll too frequent, retry later")

// Biz 是 MFA 业务。
type Biz struct {
	challenges mfa.ChallengeStore
}

// New 构造 MFA Biz。
func New(challenges mfa.ChallengeStore) *Biz {
	return &Biz{challenges: challenges}
}

// SetChallenges 运行期注入挑战存储（nil 不覆盖）。
func (b *Biz) SetChallenges(cs mfa.ChallengeStore) {
	if cs != nil {
		b.challenges = cs
	}
}

// factorID 构造因子业务键（tenant:user:method），与 model 注释一致。
func factorID(tenantID, userID string, method authmodel.MFAMethod) string {
	return tenantID + ":" + userID + ":" + string(method)
}

// EnrolledMethod 是已注册因子（对外结构，不含 secret）。
type EnrolledMethod struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	Display    string `json:"display"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
}

// Status 是 MFA 总览。
type Status struct {
	Enabled  bool             `json:"enabled"`
	Enforced string           `json:"enforcement"` // NOT_REQUIRED / OPTIONAL / REQUIRED
	Enrolled []EnrolledMethod `json:"enrolled,omitempty"`
}

// listFactors 列出某用户的全部因子（租户隔离由 Store 自动注入，此处显式传租户
// 是因为 MFA 管理面可能由管理员操作他人——需要跨用户查询能力）。
func (b *Biz) listFactors(ctx context.Context, tenantID, userID string) ([]*authmodel.UserMFAFactor, error) {
	items, _, err := bootstrappkg.MFAFactorStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{
			store.Eq("tenant_id", tenantID),
			store.Eq("user_id", userID),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("mfa: list factors: %w", err)
	}
	return items, nil
}

// GetStatus 查询 MFA 总览（源 GetMFAStatus）。
//
// 语义对齐源：只要有 enabled 的 TOTP 因子，就报 enabled=true 且
// enforcement=REQUIRED（源 `mfa_service.go:99-113`）。
func (b *Biz) GetStatus(ctx context.Context, tenantID, userID string) (*Status, error) {
	factors, err := b.listFactors(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	hasTOTP := false
	for _, f := range factors {
		if f.Method == authmodel.MFAMethodTOTP && f.Enabled {
			hasTOTP = true
			break
		}
	}
	res := &Status{Enabled: hasTOTP, Enforced: "NOT_REQUIRED"}
	if hasTOTP {
		res.Enforced = "REQUIRED"
		res.Enrolled = toEnrolled(factors)
	}
	return res, nil
}

// ListEnrolled 列出已注册因子（源 ListEnrolledMethods）。
func (b *Biz) ListEnrolled(ctx context.Context, tenantID, userID string) ([]EnrolledMethod, error) {
	factors, err := b.listFactors(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return toEnrolled(factors), nil
}

func toEnrolled(factors []*authmodel.UserMFAFactor) []EnrolledMethod {
	out := make([]EnrolledMethod, 0, len(factors))
	for _, f := range factors {
		em := EnrolledMethod{
			ID: f.ID, Method: string(f.Method), Display: f.Display, Enabled: f.Enabled,
		}
		if !f.CreatedAt.IsZero() {
			em.CreatedAt = f.CreatedAt.Unix()
		}
		if f.LastUsedAt != nil {
			em.LastUsedAt = f.LastUsedAt.Unix()
		}
		out = append(out, em)
	}
	return out
}

// StartEnrollResult 是开始注册的返回。
type StartEnrollResult struct {
	OperationID string `json:"operation_id"`
	Secret      string `json:"secret"` // 仅此一次返回
	OTPAuthURL  string `json:"otp_auth_url"`
	ExpiresAt   int64  `json:"expires_at"`
}

// StartEnroll 开始注册 TOTP（源 StartEnrollMethod）。
//
// 三条预检（对齐源）：
//  1. 冷却（30s，防循环 Start 塞 Redis）；
//  2. 已绑定则拒绝（需先 Disable——源 `mfa_service.go:140-148`）；
//  3. 生成密钥并存入挑战（TTL 10 分钟）。
func (b *Biz) StartEnroll(ctx context.Context, tenantID, userID string) (*StartEnrollResult, error) {
	if b.challenges == nil {
		return nil, ErrUnimplemented // 无存储：MFA 不可用（fail-closed 语义由 handler 映射 503）
	}
	// 1) 冷却（Redis 故障时 fail-closed）。
	if rs, ok := b.challenges.(*mfa.RedisChallengeStore); ok {
		if !rs.TryAcquireEnrollCooldown(ctx, tenantID, userID) {
			return nil, ErrTooFrequent
		}
	}
	// 2) 已绑定预检。
	if has, err := b.hasEnabledTOTP(ctx, tenantID, userID); err != nil {
		return nil, err
	} else if has {
		return nil, mfa.ErrEnrollExists
	}
	// 3) 生成密钥 + 存挑战。
	key, err := mfa.GenerateTOTP(userID)
	if err != nil {
		return nil, err
	}
	opID, err := b.challenges.SetEnroll(ctx, mfa.EnrollContext{
		TenantID: tenantID, UserID: userID, Secret: key.Secret,
	})
	if err != nil {
		return nil, fmt.Errorf("mfa: set enroll challenge: %w", err)
	}
	return &StartEnrollResult{
		OperationID: opID,
		Secret:      key.Secret,
		OTPAuthURL:  key.URL,
		ExpiresAt:   time.Now().Add(mfa.EnrollTTL).Unix(),
	}, nil
}

// ConfirmEnroll 确认注册（源 ConfirmEnrollMethod）。
//
// 与登录挑战的**关键差异**：此处用 Peek（**不消耗**）——允许首码输错重试
// （源 `mfa_service.go:190-193` 注释：登录挑战失败必须作废防暴破，注册有登录态
// 且重试面由 operation TTL + 已绑定预检 + 唯一索引共同约束）。
//
// 跨用户劫持防护：挑战上下文绑定的人必须等于当前操作者（源同此）。
func (b *Biz) ConfirmEnroll(ctx context.Context, tenantID, userID, opID, code, display string) (string, error) {
	if b.challenges == nil {
		return "", ErrUnimplemented
	}
	ec, err := b.challenges.PeekEnroll(ctx, opID)
	if err != nil {
		return "", err
	}
	if ec.TenantID != tenantID || ec.UserID != userID {
		return "", mfa.ErrUserMismatch
	}
	if !mfa.ValidateTOTP(code, ec.Secret) {
		return "", mfa.ErrInvalidCode
	}
	f := &authmodel.UserMFAFactor{
		ID:       factorID(tenantID, userID, authmodel.MFAMethodTOTP),
		TenantID: tenantID,
		UserID:   userID,
		Method:   authmodel.MFAMethodTOTP,
		Secret:   ec.Secret,
		Display:  display,
		Enabled:  true,
	}
	if err := bootstrappkg.MFAFactorStore.Create(ctx, f); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return "", mfa.ErrEnrollExists
		}
		return "", fmt.Errorf("mfa: create factor: %w", err)
	}
	b.challenges.DeleteEnroll(ctx, opID)
	return f.ID, nil
}

// Disable 禁用/移除因子（源 DisableMFA）。
// 不传 credentialID 时按 method 清空该用户全部该方法因子（源语义）。
func (b *Biz) Disable(ctx context.Context, tenantID, userID, credentialID string) error {
	w := &store.Where{Filters: []*storev1.FilterCondition{
		store.Eq("tenant_id", tenantID),
		store.Eq("user_id", userID),
	}}
	if credentialID != "" {
		w.Filters = append(w.Filters, store.Eq("id", credentialID))
	}
	// **容忍 0 行匹配**：不传 credentialID 时这是**集合删除**（清空该用户全部
	// 该方法因子）。`Store.Delete` 对 0 行返回 ErrNotFound——那是「按主键删单条」
	// 的语义，用在集合删除上会误报：用户禁用**本就未启用**的方法时，删 0 行
	// 是合法结果。（此缺陷由 Wave 2.5 的同不变量排查捕获，见 t_w1_5 回归测试。）
	if err := bootstrappkg.MFAFactorStore.Delete(ctx, w); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("mfa: disable factor: %w", err)
	}
	return nil
}

// RevokeDevice 按凭证 id 撤销（源 RevokeMFADevice）。
func (b *Biz) RevokeDevice(ctx context.Context, tenantID, userID, credentialID string) error {
	if credentialID == "" {
		return fmt.Errorf("mfa: credential_id required")
	}
	w := &store.Where{Filters: []*storev1.FilterCondition{
		store.Eq("tenant_id", tenantID),
		store.Eq("user_id", userID),
		store.Eq("id", credentialID),
	}}
	if err := bootstrappkg.MFAFactorStore.Delete(ctx, w); err != nil {
		return fmt.Errorf("mfa: revoke device: %w", err)
	}
	return nil
}

// StartChallenge 发起登录 MFA 挑战。
//
// **源未实现**（`mfa_service.go` 文件头：StartMFAChallenge 返回 UNIMPLEMENTED）。
// TOTP 本就无需服务端 challenge（前端直接提示输入），源故未实现。
func (b *Biz) StartChallenge(ctx context.Context, tenantID, userID string) (string, error) {
	return "", ErrUnimplemented
}

// VerifyChallenge 验证登录 MFA 挑战（源 VerifyMFAChallenge）。
//
// 语义：Peek 不消耗（允许失败重试）→ 校验 → 通过则**原子 Take**（防双花）
// → 更新 last_used_at。失败达上限则作废挑战。
func (b *Biz) VerifyChallenge(ctx context.Context, opID, code string) (*authmodel.UserMFAFactor, error) {
	if b.challenges == nil {
		return nil, ErrUnimplemented
	}
	lc, err := b.challenges.PeekLogin(ctx, opID)
	if err != nil {
		return nil, err
	}
	// 取该用户 enabled 的 TOTP 因子。
	factors, err := b.listFactors(ctx, lc.TenantID, lc.UserID)
	if err != nil {
		return nil, err
	}
	var target *authmodel.UserMFAFactor
	for _, f := range factors {
		if f.Method == authmodel.MFAMethodTOTP && f.Enabled {
			target = f
			break
		}
	}
	if target == nil {
		// 因子缺失（验证期间被解绑/管理员重置）：作废挑战，强制重新登录。
		b.challenges.TakeLoginAtomic(ctx, opID)
		return nil, fmt.Errorf("mfa: no enabled totp factor")
	}
	if !mfa.ValidateTOTP(code, target.Secret) {
		if b.challenges.RecordLoginFailure(ctx, opID) {
			return nil, fmt.Errorf("mfa: too many invalid attempts, please login again")
		}
		return nil, mfa.ErrInvalidCode
	}
	// 通过：原子消耗（并发同 opID 仅一个成功）。
	if !b.challenges.TakeLoginAtomic(ctx, opID) {
		return nil, fmt.Errorf("mfa: challenge already consumed")
	}
	now := time.Now()
	target.LastUsedAt = &now
	_ = bootstrappkg.MFAFactorStore.Update(ctx, target) // best-effort
	return target, nil
}

// hasEnabledTOTP 检查用户是否已绑定启用的 TOTP。
func (b *Biz) hasEnabledTOTP(ctx context.Context, tenantID, userID string) (bool, error) {
	factors, err := b.listFactors(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	for _, f := range factors {
		if f.Method == authmodel.MFAMethodTOTP && f.Enabled {
			return true, nil
		}
	}
	return false, nil
}
