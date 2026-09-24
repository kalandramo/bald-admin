// Package identity 实现 identity 扩展域（Wave 1.6）：
// user_credential（10 rpc）+ login_policy（6 rpc）+ user_profile（2 rpc，源空实现）。
//
// 复刻范围对齐源：
//   - user_credential / login_policy 源有完整实现（薄 CRUD 包装 + 值校验）；
//   - user_profile 的 BindContact/VerifyContact **源是空实现**（`return nil,nil`，
//     已复认 `user_profile_service.go:200-207`）——本实现同样返回 ErrUnimplemented，
//     不自创短信/邮件验证（那需要真实短信/邮件网关，属造 demo）。
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/store"
	"golang.org/x/crypto/bcrypt"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald-admin/internal/security/loginpolicy"
)

// ErrUnimplemented 源未实现（对齐源行为，不自创）。
var ErrUnimplemented = errors.New("identity: not implemented in source project")

// ErrNotFound 资源不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 资源已存在。
var ErrConflict = store.ErrConflict

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("identity: validation failed")

// Biz 是 identity 扩展业务。
type Biz struct{}

// New 构造 Biz。
func New() *Biz { return &Biz{} }

// ---- user_credential ----

// Credential 是对外的凭证结构。
type Credential struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	TenantID       string `json:"tenant_id"`
	IdentityType   string `json:"identity_type"`
	Identifier     string `json:"identifier"`
	CredentialType string `json:"credential_type"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at,omitempty"`
}

// credID 构造凭证业务键。
func credID(identityType, identifier string) string {
	return identityType + ":" + identifier
}

// CreateCredential 创建凭证（源 Create）。
//
// 安全处理：**密码类凭证（PASSWORD_HASH）自动 bcrypt 哈希**——若调用方传的是
// 明文密码。这是本实现的加固：源把该责任交给 repo 层，但「忘记哈希」的后果
// 极严重（明文落库），故在 biz 层强制。
func (b *Biz) CreateCredential(ctx context.Context, c Credential, plainPassword string) (*Credential, error) {
	if c.IdentityType == "" || c.Identifier == "" {
		return nil, fmt.Errorf("%w: identity_type and identifier required", ErrValidation)
	}
	cred := c.CredentialType
	if cred == "" {
		cred = "PASSWORD_HASH"
	}
	stored := ""
	switch {
	case plainPassword != "":
		// 有明文密码：强制哈希（不信任调用方已哈希）。
		h, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("identity: hash credential: %w", err)
		}
		stored = string(h)
	case cred == "PASSWORD_HASH":
		return nil, fmt.Errorf("%w: plain password required for PASSWORD_HASH credential", ErrValidation)
	default:
		return nil, fmt.Errorf("%w: credential value required", ErrValidation)
	}

	status := c.Status
	if status == "" {
		status = "ENABLED"
	}
	m := &authmodel.UserCredential{
		ID:             credID(c.IdentityType, c.Identifier),
		TenantID:       c.TenantID,
		UserID:         c.UserID,
		IdentityType:   c.IdentityType,
		Identifier:     c.Identifier,
		CredentialType: cred,
		Credential:     stored,
		Status:         status,
	}
	if err := bootstrappkg.CredentialStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: credential already exists", ErrConflict)
		}
		return nil, fmt.Errorf("identity: create credential: %w", err)
	}
	return toCredential(m), nil
}

// GetCredential 按 id 查询（源 Get）。
func (b *Biz) GetCredential(ctx context.Context, id string) (*Credential, error) {
	m, err := bootstrappkg.CredentialStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toCredential(m), nil
}

// GetCredentialByIdentifier 按身份类型 + 标识查询（源 GetByIdentifier）。
// 这是登录流程的核心查询：用「邮箱+密码」登录时先按 identifier 找到凭证。
func (b *Biz) GetCredentialByIdentifier(ctx context.Context, identityType, identifier string) (*Credential, error) {
	m, err := bootstrappkg.CredentialStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{
			store.Eq("identity_type", identityType),
			store.Eq("identifier", identifier),
		},
	})
	if err != nil {
		return nil, err
	}
	return toCredential(m), nil
}

// ListCredentials 列出某用户的凭证（源 List）。
func (b *Biz) ListCredentials(ctx context.Context, userID string) ([]Credential, error) {
	items, _, err := bootstrappkg.CredentialStore.List(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("user_id", userID)},
	})
	if err != nil {
		return nil, fmt.Errorf("identity: list credentials: %w", err)
	}
	out := make([]Credential, 0, len(items))
	for _, m := range items {
		out = append(out, *toCredential(m))
	}
	return out, nil
}

// VerifyCredential 校验凭证（源 VerifyCredential）——**密码校验的真实入口**。
//
// 语义：按 identifier 找凭证 → bcrypt 比对 → 返回结果。
// 状态检查：非 ENABLED 的凭证一律拒绝（源 Status 语义：DISABLED/BLOCKED 等
// 都不允许认证）。
func (b *Biz) VerifyCredential(ctx context.Context, identityType, identifier, plainPassword string) (bool, string, error) {
	c, err := b.GetCredentialByIdentifier(ctx, identityType, identifier)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, "", nil // 凭证不存在：业务结果，非故障
		}
		return false, "", err
	}
	if c.Status != "ENABLED" {
		return false, c.UserID, nil // 状态不允许认证
	}
	m, err := bootstrappkg.CredentialStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", c.ID)},
	})
	if err != nil {
		return false, "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(m.Credential), []byte(plainPassword)) != nil {
		return false, c.UserID, nil
	}
	return true, c.UserID, nil
}

// ChangeCredential 修改凭证（源 ChangeCredential）：需提供旧密码。
func (b *Biz) ChangeCredential(ctx context.Context, identityType, identifier, oldPassword, newPassword string) error {
	ok, _, err := b.VerifyCredential(ctx, identityType, identifier, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: old credential verification failed", ErrValidation)
	}
	return b.setCredential(ctx, identityType, identifier, newPassword)
}

// ResetCredential 重置凭证（源 ResetCredential）：**不需要旧密码**——
// 管理员/找回流程用，故权限必须由调用方（authz）严格限制。
func (b *Biz) ResetCredential(ctx context.Context, identityType, identifier, newPassword string) error {
	if _, err := b.GetCredentialByIdentifier(ctx, identityType, identifier); err != nil {
		return err
	}
	return b.setCredential(ctx, identityType, identifier, newPassword)
}

func (b *Biz) setCredential(ctx context.Context, identityType, identifier, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("%w: password too short (min 8)", ErrValidation)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("identity: hash password: %w", err)
	}
	m, err := bootstrappkg.CredentialStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{
			store.Eq("identity_type", identityType),
			store.Eq("identifier", identifier),
		},
	})
	if err != nil {
		return err
	}
	m.Credential = string(h)
	if _, err := bootstrappkg.CredentialStore.Update(ctx, m); err != nil {
		return fmt.Errorf("identity: update credential: %w", err)
	}
	return nil
}

// DeleteCredential 删除凭证（源 Delete）。
func (b *Biz) DeleteCredential(ctx context.Context, id string) error {
	if _, err := bootstrappkg.CredentialStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("identity: delete credential: %w", err)
	}
	return nil
}

func toCredential(m *authmodel.UserCredential) *Credential {
	c := &Credential{
		ID: m.ID, UserID: m.UserID, TenantID: m.TenantID,
		IdentityType: m.IdentityType, Identifier: m.Identifier,
		CredentialType: m.CredentialType, Status: m.Status,
	}
	if !m.CreatedAt.IsZero() {
		c.CreatedAt = m.CreatedAt.Unix()
	}
	return c
}

// ---- login_policy ----

// Policy 是对外的登录策略结构。
type Policy struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	TargetID  string `json:"target_id,omitempty"`
	Type      string `json:"type"`
	Method    string `json:"method"`
	Value     string `json:"value"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// policyID 构造策略业务键。
func policyID(tenantID, ptype, method, value string) string {
	return strings.Join([]string{tenantID, ptype, method, value}, ":")
}

// CreatePolicy 创建登录策略（源 Create）。
//
// **关键校验**（对齐源）：值格式 + 类型合法——白名单配错格式会导致
// 匹配器静默不命中 → 全员被锁（源注释原话）。
func (b *Biz) CreatePolicy(ctx context.Context, tenantID, operatorID string, p Policy) (*Policy, error) {
	if err := loginpolicy.ValidateType(p.Type); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrValidation, err)
	}
	if err := loginpolicy.ValidateValue(p.Method, p.Value); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrValidation, err)
	}
	m := &authmodel.LoginPolicy{
		ID:        policyID(tenantID, strings.ToUpper(p.Type), strings.ToUpper(p.Method), p.Value),
		TenantID:  tenantID,
		TargetID:  p.TargetID,
		Type:      strings.ToUpper(p.Type),
		Method:    strings.ToUpper(p.Method),
		Value:     p.Value,
		Reason:    p.Reason,
		CreatedBy: operatorID,
		UpdatedBy: operatorID,
	}
	if err := bootstrappkg.LoginPolicyStore.Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: policy already exists", ErrConflict)
		}
		return nil, fmt.Errorf("identity: create policy: %w", err)
	}
	return toPolicy(m), nil
}

// ListPolicies 列出策略（源 List）。
func (b *Biz) ListPolicies(ctx context.Context, tenantID string) ([]Policy, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	items, _, err := bootstrappkg.LoginPolicyStore.List(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("identity: list policies: %w", err)
	}
	out := make([]Policy, 0, len(items))
	for _, m := range items {
		out = append(out, *toPolicy(m))
	}
	return out, nil
}

// GetPolicy 按 id 查询（源 Get）。
func (b *Biz) GetPolicy(ctx context.Context, id string) (*Policy, error) {
	m, err := bootstrappkg.LoginPolicyStore.Get(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	})
	if err != nil {
		return nil, err
	}
	return toPolicy(m), nil
}

// DeletePolicy 删除策略（源 Delete）。
func (b *Biz) DeletePolicy(ctx context.Context, id string) error {
	if _, err := bootstrappkg.LoginPolicyStore.Delete(ctx, &store.Where{
		Filters: []*storev1.FilterCondition{store.Eq("id", id)},
	}); err != nil {
		return fmt.Errorf("identity: delete policy: %w", err)
	}
	return nil
}

// CountPolicies 统计（源 Count）。
func (b *Biz) CountPolicies(ctx context.Context, tenantID string) (int64, error) {
	w := &store.Where{}
	if tenantID != "" {
		w.Filters = append(w.Filters, store.Eq("tenant_id", tenantID))
	}
	n, err := bootstrappkg.LoginPolicyStore.Count(ctx, w)
	if err != nil {
		return 0, fmt.Errorf("identity: count policies: %w", err)
	}
	return n, nil
}

func toPolicy(m *authmodel.LoginPolicy) *Policy {
	p := &Policy{
		ID: m.ID, TenantID: m.TenantID, TargetID: m.TargetID,
		Type: m.Type, Method: m.Method, Value: m.Value, Reason: m.Reason,
	}
	if !m.CreatedAt.IsZero() {
		p.CreatedAt = m.CreatedAt.Unix()
	}
	return p
}

// ---- user_profile（源空实现）----

// BindContact 绑定联系方式——**源是空实现**（`return nil,nil`，已复认）。
// 本实现显式返回 ErrUnimplemented，理由：真实实现需短信/邮件网关，
// 自创即为造 demo。
func (b *Biz) BindContact(ctx context.Context, userID, contact string) error {
	return ErrUnimplemented
}

// VerifyContact 验证联系方式——**源是空实现**（同上）。
func (b *Biz) VerifyContact(ctx context.Context, userID, code string) error {
	return ErrUnimplemented
}

// 保留 time 引用（Extra 字段的未来扩展）。
var _ = time.Now
