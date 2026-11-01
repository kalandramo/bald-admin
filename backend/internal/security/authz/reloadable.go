// Package authz 提供授权器的**应用层装饰器**。
//
// ReloadableAuthorizer 持有一个可整体替换的内层授权器：casbin 的 g 行
// （subject→角色绑定）在构造期经 bootstrap.loadPolicyCSV 从 UserStore 一次性
// 装载，运行期新增用户不在其中——注册后立即登录会 403 直到重启进程。业务在
// 用户/角色变更后调用 Reload 重建策略快照即可让变更即时生效。
//
// 为什么用「整体替换」而非「增量添加策略」：
//   - 增量需要底层暴露 AddPolicy 等 API；
//   - 整体替换的语义更简单可靠（策略真源是 DB，重建即与 DB 一致）；
//   - 重建成本 = 一次全表读 + casbin 装载，对低频的注册/改角色操作可接受。
//
// 与 bald 的 authz.ReloadableAuthorizer 的关系：框架接口只定义能力契约；
// contrib/authz-casbin 的 Reload 重载构造期注入的同一策略源，而本应用的真源
// 是 DB，故仍需本装饰器承担「重建」语义，并实现该接口供能力发现。
package authz

import (
	"context"
	"sync"

	"github.com/kalandramo/bald/pkg/authz"
)

// ReloadableAuthorizer 是可热重载的授权器装饰器。
//
// 并发安全：Authorize 读、Reload 写，用 RWMutex 保护。读路径（每次请求）用
// RLock，不阻塞并发请求；Reload（低频）用 Lock。
type ReloadableAuthorizer struct {
	mu    sync.RWMutex
	inner authz.Authorizer
	// build 是策略重建函数（从真源重新构造授权器）。nil 时 Reload 无操作。
	build func() (authz.Authorizer, error)
}

// NewReloadable 用初始授权器与重建函数构造装饰器。
func NewReloadable(initial authz.Authorizer, build func() (authz.Authorizer, error)) *ReloadableAuthorizer {
	return &ReloadableAuthorizer{inner: initial, build: build}
}

// Authorize 实现 authz.Authorizer：委托给当前内层授权器。
func (r *ReloadableAuthorizer) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	r.mu.RLock()
	inner := r.inner
	r.mu.RUnlock()
	if inner == nil {
		// 无授权器：**fail-closed**（拒绝）——授权是安全边界，
		// 缺失时不能放行（与 casbin 空策略默认拒绝同语义）。
		return false, nil
	}
	return inner.Authorize(ctx, subject, object, action)
}

// Reload 从真源重建策略并原子替换。
// 重建失败时**保留旧授权器**（不置空）——避免「重建失败导致全部请求 403」
// 的可用性事故；返回错误供调用方记录。
func (r *ReloadableAuthorizer) Reload() error {
	if r.build == nil {
		return nil
	}
	next, err := r.build()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.inner = next
	r.mu.Unlock()
	return nil
}

// 编译期契约：装饰器是合法 Authorizer，且可被 authz.Reload 能力发现。
var (
	_ authz.Authorizer           = (*ReloadableAuthorizer)(nil)
	_ authz.ReloadableAuthorizer = (*ReloadableAuthorizer)(nil)
)
