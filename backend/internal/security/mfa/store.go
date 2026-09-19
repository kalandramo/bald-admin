package mfa

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// TTL 常量（与源 data.MfaChallengeTTL 对齐）。
const (
	// EnrollTTL 是注册挑战有效期。
	EnrollTTL = 10 * time.Minute
	// LoginTTL 是登录挑战有效期。
	LoginTTL = 5 * time.Minute
	// EnrollCooldown 是注册发起冷却（防循环 Start 塞 Redis，源同此语义）。
	EnrollCooldown = 30 * time.Second
	// MaxLoginFailures 是登录挑战最大失败次数（超过即作废挑战，防暴破）。
	// 6 位码空间 10^6，5 次失败后作废已足够安全。
	MaxLoginFailures = 5
)

func keyEnroll(opID string) string  { return "mfa:enroll:" + opID }
func keyLogin(opID string) string   { return "mfa:login:" + opID }
func keyFail(opID string) string    { return "mfa:fail:" + opID }
func keyCooldown(t, u string) string { return "mfa:cd:" + t + ":" + u }

// RedisChallengeStore 是基于 Redis 的 ChallengeStore 实现。
//
// 为什么用 Redis 而非进程内存：多实例部署下 Start 与 Confirm/Verify 可能落在
// 不同实例（负载均衡），进程内存会导致「A 实例发起、B 实例校验」必然失败。
type RedisChallengeStore struct {
	rdb goredis.UniversalClient
}

// NewRedisChallengeStore 构造 Redis 挑战存储。
func NewRedisChallengeStore(rdb goredis.UniversalClient) *RedisChallengeStore {
	return &RedisChallengeStore{rdb: rdb}
}

// SetEnroll 实现 ChallengeStore。
func (s *RedisChallengeStore) SetEnroll(ctx context.Context, ec EnrollContext) (string, error) {
	opID := NewOperationID()
	raw, err := json.Marshal(ec)
	if err != nil {
		return "", fmt.Errorf("mfa: marshal enroll ctx: %w", err)
	}
	if err := s.rdb.Set(ctx, keyEnroll(opID), raw, EnrollTTL).Err(); err != nil {
		return "", fmt.Errorf("mfa: set enroll: %w", err)
	}
	return opID, nil
}

// PeekEnroll 实现 ChallengeStore：**不消耗**（允许首码输错重试）。
func (s *RedisChallengeStore) PeekEnroll(ctx context.Context, opID string) (*EnrollContext, error) {
	raw, err := s.rdb.Get(ctx, keyEnroll(opID)).Bytes()
	if err != nil {
		return nil, ErrOperationInvalid
	}
	var ec EnrollContext
	if err := json.Unmarshal(raw, &ec); err != nil {
		return nil, fmt.Errorf("mfa: unmarshal enroll ctx: %w", err)
	}
	return &ec, nil
}

// DeleteEnroll 实现 ChallengeStore。
func (s *RedisChallengeStore) DeleteEnroll(ctx context.Context, opID string) {
	s.rdb.Del(ctx, keyEnroll(opID))
}

// SetLogin 实现 ChallengeStore。
func (s *RedisChallengeStore) SetLogin(ctx context.Context, lc LoginChallengeContext) (string, error) {
	opID := NewOperationID()
	raw, err := json.Marshal(lc)
	if err != nil {
		return "", fmt.Errorf("mfa: marshal login ctx: %w", err)
	}
	if err := s.rdb.Set(ctx, keyLogin(opID), raw, LoginTTL).Err(); err != nil {
		return "", fmt.Errorf("mfa: set login: %w", err)
	}
	return opID, nil
}

// PeekLogin 实现 ChallengeStore：**不消耗**（允许失败重试至上限）。
func (s *RedisChallengeStore) PeekLogin(ctx context.Context, opID string) (*LoginChallengeContext, error) {
	raw, err := s.rdb.Get(ctx, keyLogin(opID)).Bytes()
	if err != nil {
		return nil, ErrOperationInvalid
	}
	var lc LoginChallengeContext
	if err := json.Unmarshal(raw, &lc); err != nil {
		return nil, fmt.Errorf("mfa: unmarshal login ctx: %w", err)
	}
	return &lc, nil
}

// RecordLoginFailure 实现 ChallengeStore：计数并返回是否达上限。
// 达上限时**作废挑战**（删除），强制用户重新走登录流程。
func (s *RedisChallengeStore) RecordLoginFailure(ctx context.Context, opID string) bool {
	n, err := s.rdb.Incr(ctx, keyFail(opID)).Result()
	if err != nil {
		return false
	}
	s.rdb.Expire(ctx, keyFail(opID), LoginTTL)
	if n >= MaxLoginFailures {
		s.rdb.Del(ctx, keyLogin(opID), keyFail(opID))
		return true
	}
	return false
}

// TakeLoginAtomic 实现 ChallengeStore：原子取出（GETDEL）——
// 并发同 opID 仅一个成功，防「一次挑战换两个 token」的双花。
func (s *RedisChallengeStore) TakeLoginAtomic(ctx context.Context, opID string) bool {
	_, err := s.rdb.GetDel(ctx, keyLogin(opID)).Result()
	return err == nil
}

// TryAcquireEnrollCooldown 尝试获取注册冷却锁（返回 false = 冷却中）。
func (s *RedisChallengeStore) TryAcquireEnrollCooldown(ctx context.Context, tenantID, userID string) bool {
	ok, err := s.rdb.SetNX(ctx, keyCooldown(tenantID, userID), "1", EnrollCooldown).Result()
	if err != nil {
		// Redis 故障：**fail-closed**（拒绝）——MFA 是安全边界。
		return false
	}
	return ok
}
