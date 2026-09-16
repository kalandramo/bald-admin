// middleware_test.go 锁定 authnMiddleware 的动态审计转发语义（T6 盲区回归）。
//
// 盲区背景：AuthnMiddleware 缺省在构造期快照全局 auditor，而本包各 Register*
// 在 main 期执行（早于契约轨 BeforeStart 装配与 R1-2 热切），快照必为 nop——
// 401 认证失败审计静默丢失。authnMiddleware 统一注入 securityaudit.Global()
// 转发器修复；本测试把「构造早于装配」的生产时序原样复现，快照回归即失败。
package gin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
)

func TestAuthnMiddleware_AuditFollowsLateGlobalSwap(t *testing.T) {
	// 1. 构造期全局 = nop（生产盲区时序：路由注册在 main 期，装配在其后）。
	audit.SetAuditor(audit.NopAuditor())
	t.Cleanup(func() { audit.SetAuditor(audit.NopAuditor()) })

	gingonic.SetMode(gingonic.TestMode)
	e := gingonic.New()
	authed := e.Group("/v1", authnMiddleware(stubAuthn{}))
	authed.GET("/probe", func(c *gingonic.Context) { c.Status(http.StatusOK) })

	// 2. 构造之后才装配后端（模拟契约轨 BeforeStart SetAuditor / 协调器热切）。
	mem := &memAuditor{}
	audit.SetAuditor(mem)

	// 3. 无 token 请求 → 401 abort → 认证失败审计必须流向当前全局后端。
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/probe", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	evs := mem.all()
	if len(evs) != 1 {
		t.Fatalf("audit events = %d, want 1 (构造期快照回归：事件进了 nop)", len(evs))
	}
	if ev := evs[0]; ev.Object != "authn" || ev.Action != "authenticate" || ev.Result != audit.ResultDeny {
		t.Fatalf("event = %+v, want {authn authenticate deny}", ev)
	}
}

// stubAuthn 恒失败认证器：驱动 authn abort 路径触发 auditAuthnFailure。
type stubAuthn struct{}

func (stubAuthn) Authenticate(context.Context) (*authn.AuthClaims, error) {
	return nil, errors.New("stub authn")
}

func (stubAuthn) AuthenticateToken(string) (*authn.AuthClaims, error) {
	return nil, errors.New("stub authn")
}

// memAuditor 内存审计后端（并发安全）。
type memAuditor struct {
	mu   sync.Mutex
	evts []audit.AuditEvent
}

func (m *memAuditor) Record(_ context.Context, ev audit.AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evts = append(m.evts, ev)
}

func (m *memAuditor) all() []audit.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]audit.AuditEvent(nil), m.evts...)
}
