package e2e

// memtoken_test.go —— 内存版 token.Store 测试替身（Wave 5.1 补）。
//
// ## 为什么需要它
//
// 吊销路径的 e2e（TestWave1d_ProductionWiring_RevocationActive 等）依赖真实
// Redis（127.0.0.1:6379）。本机/CI 无 Redis 时它们**整体 SKIP**——于是「吊销
// 检查是否真的接入中间件」这条安全不变量，在无 Redis 环境下**从未被执行**。
// 这是本轮实测踩到的坑：把 SKIP 当成了 PASS 上报。
//
// token.Store 是接口（见 internal/security/token/token.go），故可注入内存实现，
// 让吊销路径在**任意环境**都被真正执行。内存实现只服务测试语义（正确性优先于
// 性能/持久化），不用于生产。

import (
	"context"
	"sync"
	"time"

	"github.com/kalandramo/bald-admin/internal/security/token"
)

// memTokenStore 是 token.Store 的内存实现：吊销名单 + 刷新令牌 + 活跃索引 +
// 拉黑记录。全部带 TTL 惰性过期（读取时判定，不做后台清理）。
type memTokenStore struct {
	mu       sync.Mutex
	revoked  map[string]time.Time // token → 过期时刻
	refresh  map[string]string    // refreshToken → subject
	access   map[string][]string  // subject → tokens
	blockTil map[string]time.Time // token → 拉黑到期（零值 = 永久）
	reason   map[string]string
}

// 编译期断言：内存实现满足 token.Store 契约（接口变更时此处先红）。
var _ token.Store = (*memTokenStore)(nil)

func newMemTokenStore() *memTokenStore {
	return &memTokenStore{
		revoked:  map[string]time.Time{},
		refresh:  map[string]string{},
		access:   map[string][]string{},
		blockTil: map[string]time.Time{},
		reason:   map[string]string{},
	}
}

// expired 判定某 TTL 记录是否已过期（零值时间 = 不过期）。
func expired(t time.Time) bool {
	return !t.IsZero() && time.Now().After(t)
}

func (m *memTokenStore) Revoke(_ context.Context, tok string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[tok] = time.Now().Add(ttl)
	return nil
}

func (m *memTokenStore) IsRevoked(_ context.Context, tok string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.revoked[tok]
	if !ok {
		return false, nil
	}
	if expired(exp) {
		delete(m.revoked, tok)
		return false, nil
	}
	return true, nil
}

func (m *memTokenStore) SaveRefresh(_ context.Context, refreshToken, subject string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 存 subject 与过期时刻（用 refresh map 存 subject，过期时刻复用 revoked 语义
	// 不便——单独用 blockTil 之外的结构会过度设计，这里直接以 refresh 承载 subject，
	// 过期由 ConsumeRefresh 的一次性语义兜底）。
	m.refresh[refreshToken] = subject
	return nil
}

func (m *memTokenStore) ConsumeRefresh(_ context.Context, refreshToken string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.refresh[refreshToken]
	if !ok {
		return "", token.ErrNotFound
	}
	delete(m.refresh, refreshToken) // 一次性语义
	return s, nil
}

func (m *memTokenStore) TrackAccess(_ context.Context, subject, tok string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.access[subject] = append(m.access[subject], tok)
	return nil
}

func (m *memTokenStore) ListAccess(_ context.Context, subject string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.access[subject]...), nil
}

func (m *memTokenStore) UntrackAccess(_ context.Context, subject, tok string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.access[subject][:0]
	for _, t := range m.access[subject] {
		if t != tok {
			kept = append(kept, t)
		}
	}
	m.access[subject] = kept
	return nil
}

func (m *memTokenStore) Block(_ context.Context, tok, reason string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ttl > 0 {
		m.blockTil[tok] = time.Now().Add(ttl)
	} else {
		m.blockTil[tok] = time.Time{} // 零值 = 永久
	}
	m.reason[tok] = reason
	return nil
}

func (m *memTokenStore) Unblock(_ context.Context, tok string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blockTil, tok)
	delete(m.reason, tok)
	return nil
}

func (m *memTokenStore) BlockedUntil(_ context.Context, tok string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blockTil[tok], nil
}
