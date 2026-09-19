// Package mfa 提供多因素认证（MFA）能力（Wave 1.5）。
//
// 对应源 go-wind-admin 的 MFAService（10 rpc，`mfa.proto`）。**复刻范围严格对齐
// 源实现**——源 `mfa_service.go` 文件头注释明载「本轮仅落地 TOTP；非 TOTP 方法、
// StartMFAChallenge、备份码相关 RPC 本轮返回 UNIMPLEMENTED」，故本包同样：
//   - 实现 TOTP 的注册/确认/校验/禁用/撤销/状态查询（7 条 rpc）；
//   - StartMFAChallenge / GenerateBackupCodes / ListBackupCodes 返回 UNIMPLEMENTED
//     （不自创 SMS/WebAuthn/备份码实现——那是「造 demo」，违背「对齐源行为」）。
//
// 框架能力验证结论：bald v0.8.1 **不提供** MFA/TOTP 组件，故本包为业务自建，
// TOTP 用与源相同的库 `github.com/pquerna/otp v1.5.0`。
//
// 三条安全语义（源实现的精髓，本包逐条对齐）：
//   - **±1 时间窗口**（skew=1，30s 周期）——容忍客户端/服务端时钟小幅漂移；
//   - **登录挑战「取出即删」vs 注册挑战「peek 不消耗」**——登录挑战失败必须作废
//     （防暴破，6 位码空间仅 10^6）；注册有登录态保护且需允许首码输错重试；
//   - **跨用户劫持防护**——注册上下文绑定的人必须等于当前操作者（源
//     `mfa_service.go:196-199` 的 enrollCtx 归属校验）。
package mfa

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// 与源对齐的常量（`mfa_service.go:38-42`）。
const (
	// Issuer 是 otpauth URI 中的发行方标识（认证器 App 内展示归属）。
	Issuer = "GoWindAdmin"
	// Skew 是允许的时间窗口偏移（±1 个 30s 周期），防时钟小幅漂移。
	Skew = 1
	// Period 是 TOTP 周期（秒）。
	Period = 30
)

// ErrInvalidCode 验证码错误。
var ErrInvalidCode = errors.New("mfa: invalid code")

// ErrEnrollExists 已绑定同方法的因子（需先解绑）。
var ErrEnrollExists = errors.New("mfa: factor already enrolled")

// ErrOperationInvalid 操作上下文不存在或已过期。
var ErrOperationInvalid = errors.New("mfa: invalid or expired operation")

// ErrUserMismatch 操作上下文归属与当前用户不符（跨用户劫持）。
var ErrUserMismatch = errors.New("mfa: operation user mismatch")

// TOTPKey 是 TOTP 密钥的生成结果。
type TOTPKey struct {
	Secret string // base32 secret
	URL    string // otpauth:// URI（供认证器扫码）
}

// GenerateTOTP 生成新的 TOTP 密钥。
// AccountName 用 userID 防认证器 App 内同名冲突（源用 `uid:%d`）。
func GenerateTOTP(userID string) (*TOTPKey, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: fmt.Sprintf("uid:%s", userID),
	})
	if err != nil {
		return nil, fmt.Errorf("mfa: generate totp key: %w", err)
	}
	return &TOTPKey{Secret: key.Secret(), URL: key.URL()}, nil
}

// ValidateTOTP 校验 TOTP 码（**±1 窗口**，30s 周期，6 位 SHA1）。
//
// 显式指定 ValidateOpts 而非用默认 Validate：默认值虽等价，但显式写出
// 让「窗口/周期/位数」这三个安全参数在代码里可见——它们变了就是安全语义变了。
func ValidateTOTP(code, secret string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    Period,
		Skew:      Skew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// NewOperationID 生成操作 id（源 newOperationID 的等价物：随机十六进制串）。
func NewOperationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// EnrollContext 是注册挑战上下文（绑定归属，防跨用户劫持）。
type EnrollContext struct {
	TenantID string
	UserID   string
	Secret   string
}

// LoginChallengeContext 是登录挑战上下文。
type LoginChallengeContext struct {
	UserID   string
	TenantID string
	Username string
}

// ChallengeStore 是 MFA 挑战/注册上下文的存储抽象。
//
// 接口按**语义**命名而非 CRUD 命名（peek/consume/take），因为源实现的关键差异
// 正是「三种消耗语义」——用名字把差异显式化，避免误用。
type ChallengeStore interface {
	// SetEnroll 保存注册挑战，返回操作 id。
	SetEnroll(ctx context.Context, ec EnrollContext) (string, error)
	// PeekEnroll 读取注册挑战（**不消耗**——允许首码输错重试）。
	PeekEnroll(ctx context.Context, opID string) (*EnrollContext, error)
	// DeleteEnroll 删除注册挑战（确认成功后清理）。
	DeleteEnroll(ctx context.Context, opID string)

	// SetLogin 保存登录挑战，返回操作 id。
	SetLogin(ctx context.Context, lc LoginChallengeContext) (string, error)
	// PeekLogin 读取登录挑战（**不消耗**——允许失败重试至上限）。
	PeekLogin(ctx context.Context, opID string) (*LoginChallengeContext, error)
	// RecordLoginFailure 记一次失败，返回是否已达上限。
	RecordLoginFailure(ctx context.Context, opID string) bool
	// TakeLoginAtomic 原子取出（通过时用——并发同 opID 仅一个成功，防双花）。
	TakeLoginAtomic(ctx context.Context, opID string) bool
}
