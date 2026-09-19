// Package authz 提供授权器的**应用层装饰器**（Wave 1d-3）。
//
// 框架能力缺口（实测，待上游确认）：
// bald 的 authz.Authorizer 接口只有 Authorize 一个方法（pkg/authz/authz.go:15-20），
// contrib/authz-casbin 的实现也只暴露 New/NewWithModel/Authorize
//（contrib/authz-casbin/casbin.go:48/55/80）——**没有任何热重载策略的入口**。
//
// 后果（本会话端到端实测）：casbin 的 g 行（subject→角色绑定）在启动时经
// bootstrap.loadPolicyCSV 从 UserStore 一次性装载。**运行期新增的用户不在其中**——
// 注册后立即登录，其权限直到**重启进程**才生效（实测：新注册用户访问 /v1/auth/whoami
// 返回 403 "access denied: subject=u-xxx, object=auth, action=get"，而同样的
// 请求在重启后通过）。这对「注册即用」是硬伤。
//
// 应用层绕行：ReloadableAuthorizer 装饰器——持有一个可整体替换的内层授权器，
// 业务在用户/角色变更后调用 Reload 重建策略快照。**框架零改动**（装饰器是
// authz.Authorizer 的合法实现，与 token.RevocationChecker 同款手法）。
//
// 为什么用「整体替换」而非「增量添加策略」：
//   - 增量需要框架暴露 AddPolicy 等 API（当前没有）；
//   - 整体替换的语义更简单可靠（策略真源是 DB，重建即与 DB 一致）；
//   - 重建成本 = 一次全表读 + casbin 装载，对低频的注册/改角色操作可接受。
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
