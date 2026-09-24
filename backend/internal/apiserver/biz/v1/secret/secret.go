// Package secret 实现 Secret 业务层（M6.3 起落库，消除 handler 硬编码返回）。
//
// 设计约束：业务层只依赖 bald 核心的 store.Store 抽象（依赖倒置），不直接耦合 gorm/gin/grpc；
// 查询自动经 store 的多租户过滤（Where.T(ctx) 由 pkg/store 注入 TenantID），业务不手写隔离 SQL。
package secret

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kalandramo/bald/cache"
	"github.com/kalandramo/bald/cache/loadable"
	"github.com/kalandramo/bald/pkg/contextx"
	"github.com/kalandramo/bald/pkg/store"

	berrs "github.com/kalandramo/bald/berrors"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// SecretBiz Secret 业务服务。
type SecretBiz struct {
	// cache 是读穿透缓存（cache/loadable 组合器）；nil = 禁用，直连 store。
	// D1 迁移：原 contrib/cache-redis 的请求期 loader 由构造期 loader 承接
	// （SetCache 时绑定，loader 从 key 反解 id/tenant）。
	cache *loadable.Cache
}

// New 构造 SecretBiz。backend 是通用 KV 缓存（cache.Cache）；非 nil 时包装为
// loadable 读穿透缓存（loader 构造期绑定，从 key 反解 id/tenant）。nil = 禁用。
//
// 注意：store 依赖不在构造期快照——wire 的 InitializeBiz 在 main 构造期执行，
// 彼时 InitBridges 尚未赋值 bootstrappkg.SecretStore，构造期快照会把 nil 固化
// 进 Biz（Get/Delete 即 nil panic，与 Signer 时序错位同款）；仓储改由请求期经
// store() 读取最新值（与 auth biz 直读包级变量同范式）。loader 闭包捕获 b 指针
// 而非 store 快照，故此处绑定是安全的。
func New(backend cache.Cache) *SecretBiz {
	b := &SecretBiz{}
	b.wrapCache(backend)
	return b
}

// wrapCache 把通用 KV 缓存包装为读穿透缓存（nil 保持禁用态）。
func (b *SecretBiz) wrapCache(backend cache.Cache) {
	if backend == nil {
		b.cache = nil
		return
	}
	b.cache = loadable.New(backend, b.loadSecret,
		loadable.WithTTL(bootstrappkg.DefaultCacheTTL),
		loadable.WithDegradeOnError(), // Redis 故障降级直连 store（T4 语义）
	)
}

// SetCache 运行期注入缓存（main.go BeforeStart 在 InitBridges 之后调用）：
// 配置驱动的 cache.redis 段（含 password/db）只流向 bootstrap.RedisCache，
// wire 的 env 通道（BALD_ADMIN_REDIS_ADDR）拿不到完整参数；构造期值拷贝又会把
// nil/禁用态固化（与 file Biz 的 SetStorage 同款时序）。nil 不覆盖（保留 env 通道）。
func (b *SecretBiz) SetCache(c cache.Cache) {
	if c == nil {
		return
	}
	b.wrapCache(c)
}

// loadSecret 是 loadable 的读取函数：从缓存键反解 id，经 store 加载并序列化。
// 键格式 CacheKey("secret", tenant, id)；租户段取自 ctx（与写键同源，防止
// 键/上下文不一致导致读错数据），故前缀校验失败即报错。
func (b *SecretBiz) loadSecret(ctx context.Context, key string) ([]byte, error) {
	tenant := contextx.TenantIDFromContext(ctx)
	id, err := bootstrappkg.CutCacheKeyPrefix(key, bootstrappkg.CacheKeyPrefix("secret", tenant))
	if err != nil {
		return nil, fmt.Errorf("secret.loadSecret: %w", err)
	}
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", id))
	s, err := b.store().Get(ctx, w.T(ctx))
	if err != nil {
		return nil, fmt.Errorf("secret.Get(%s): %w", id, err)
	}
	buf, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("secret.Get marshal: %w", err)
	}
	return buf, nil
}

// store 请求期解析仓储（读 bootstrap 包级桥接最新值）。
func (b *SecretBiz) store() *store.Store[authmodel.Secret] { return bootstrappkg.SecretStore }

// Item 是单个 Secret 的展示结构（供 handler 序列化）。
type Item struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TenantID string `json:"tenant_id"`
}

// Get 按 ID 取 Secret，自动受调用方 ctx 中的租户隔离约束（M3/M4 多租户）。
// M6.2 起经 Cache-Aside 读 Redis（命中返缓存，未命中加载并回填）；cache 禁用时直连 store。
// D1：cache 为 loadable 组合器，未命中经 loadSecret 回填（键由本方法构造）。
func (b *SecretBiz) Get(ctx context.Context, id string) (*Item, error) {
	tenant := contextx.TenantIDFromContext(ctx)
	key := bootstrappkg.CacheKey("secret", tenant, id)

	var (
		raw []byte
		err error
	)
	if b.cache != nil {
		raw, err = b.cache.Get(ctx, key)
	} else {
		raw, err = b.loadSecret(ctx, key)
	}
	if err != nil {
		// NotFound 哨兵升级为 berrors（决策⑧：gin/gRPC/gateway 三面直达同一
		// code/reason，无需各面再转换）；其余错误原样上抛（内部错误归 500）。
		if berrs.Is(err, store.ErrNotFound) {
			return nil, berrs.NotFound("secret/not_found").WithCause(err)
		}
		return nil, err
	}
	var s authmodel.Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("secret.Get unmarshal: %w", err)
	}
	return &Item{ID: s.ID, Name: s.Name, Content: s.Content, TenantID: s.TenantID}, nil
}

// List 列出调用方租户下的全部 Secret。
func (b *SecretBiz) List(ctx context.Context) ([]*Item, error) {
	ss, _, err := b.store().List(ctx, (&store.Where{}).T(ctx))
	if err != nil {
		return nil, fmt.Errorf("secret.List: %w", err)
	}
	items := make([]*Item, 0, len(ss))
	for _, s := range ss {
		items = append(items, &Item{ID: s.ID, Name: s.Name, Content: s.Content, TenantID: s.TenantID})
	}
	return items, nil
}

// Delete 删除调用方租户下的指定 Secret（自动受 ctx 租户隔离约束，跨租户删除被 store 拦为 NotFound）。
// 同时清理 Cache-Aside 缓存条目；cache 禁用时忽略。返回是否实际删除（true=命中并删除）。
func (b *SecretBiz) Delete(ctx context.Context, id string) (bool, error) {
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", id))
	w = w.T(ctx)

	// 先确认存在（命中租户隔离），不存在按 NotFound 处理。
	if _, err := b.store().Get(ctx, w); err != nil {
		return false, fmt.Errorf("secret.Delete(%s): %w", id, err)
	}
	if _, err := b.store().Delete(ctx, w); err != nil {
		return false, fmt.Errorf("secret.Delete(%s): %w", id, err)
	}
	if b.cache != nil {
		tenant := contextx.TenantIDFromContext(ctx)
		_ = b.cache.Delete(ctx, bootstrappkg.CacheKey("secret", tenant, id))
	}
	return true, nil
}
