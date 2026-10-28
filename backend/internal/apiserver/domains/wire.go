// Package domains 持有各域装配声明包（同级 <name>d 子包）共享的装配小工具：
// Wire/Routes 守卫（AssertGin/RequireStore）与 biz+routes 通用拓扑
// （StandardDomain，见 standard.go）。
package domains

import (
	"fmt"
	"net/http"

	gingonic "github.com/gin-gonic/gin"
)

// AssertGin 断言 Routes 收到的 HTTP 面是 *gin.Engine——各纯 gin 域的共用守卫。
// domain 仅用于错误文案前缀：文案与各域原实现逐字一致（e2e/守卫可能依赖）。
func AssertGin(domain string, h http.Handler) (*gingonic.Engine, error) {
	e, ok := h.(*gingonic.Engine)
	if !ok {
		return nil, fmt.Errorf("%s: HTTP face is %T, want *gin.Engine (the domain owns its router coupling)", domain, h)
	}
	return e, nil
}

// RequireStore 校验域仓储 getter 的产物非 nil（「未就绪」错误）——域 Wire 晚于
// 业务 BeforeStart 的时序守卫（getter 迟到取用语义见 tenantd 包头注）。
func RequireStore[T any](domain, what string, st *T) (*T, error) {
	if st == nil {
		return nil, fmt.Errorf("%s: %s not ready (InitBridges must run in business BeforeStart before domain Wire)", domain, what)
	}
	return st, nil
}
