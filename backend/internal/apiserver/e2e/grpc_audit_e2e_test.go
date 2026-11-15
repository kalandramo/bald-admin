package e2e

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/middleware"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// memAuditor 是 e2e 断言用内存 Auditor。
type memAuditor struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (m *memAuditor) Record(_ context.Context, e audit.AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *memAuditor) all() []audit.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]audit.AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}

// invokeWithAudit 串联 Authn + Authz + Audit拦截器，返回 (err, 审计事件)。
func invokeWithAudit(t *testing.T, a *memAuditor, token, fullMethod string) error {
	t.Helper()
	if _, err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	repos = bootstrappkg.LastRepositories()
	info := &grpc.UnaryServerInfo{FullMethod: fullMethod}
	handler := func(c context.Context, _ any) (any, error) { return "ok", nil }
	ctx := grpcAuthCtx(token)
	// 链路顺序：Authn(外层) → Audit → Authz → handler(内层)。
	//  - Audit 在 Authn 内侧：能读取认证注入的 subject/tenant；
	//  - Audit 在 Authz 外侧：持有 **未收口** 的 berrors，故能识别
	//    PermissionDenied → ResultDeny（若各层各自过桥，错误已被转成
	//    status error，统一 Audit 将无法判定 deny）。
	chain := middleware.UnaryServerInterceptor(middleware.Chain(
		middleware.Authn(bootstrappkg.Authenticator),
		middleware.Audit(
			middleware.AuditWithAuditor(a),
			middleware.AuditWithObjectResolver(authz.DefaultGRPCObject),
			middleware.AuditWithActionResolver(authz.DefaultGRPCAction)),
		middleware.Authz(bootstrappkg.Authorizer,
			middleware.WithObjectResolver(authz.DefaultGRPCObject),
			middleware.WithActionResolver(authz.DefaultGRPCAction)),
	))
	_, err := chain(ctx, nil, info, handler)
	return err
}

// TestGRPCAudit_Allow 授权通过的 gRPC 调用应产生一条 allow 审计事件，含归一化三元组。
func TestGRPCAudit_Allow(t *testing.T) {
	if _, err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	repos = bootstrappkg.LastRepositories()
	a := &memAuditor{}
	tok := issueToken(t, "admin", "u-admin", "admin")
	if err := invokeWithAudit(t, a, tok, "/go.bald.admin.v1.SecretService/GetSecret"); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
	evs := a.all()
	if len(evs) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Subject != "u-admin" || ev.TenantID != "t-default" {
		t.Errorf("subject/tenant mismatch: %+v", ev)
	}
	if ev.Object != "secret" || ev.Action != "get" {
		t.Errorf("normalized (object,action) mismatch: (%q,%q)", ev.Object, ev.Action)
	}
	if ev.Result != audit.ResultAllow {
		t.Errorf("result should be allow, got %q", ev.Result)
	}
}

// TestGRPCAudit_Deny 授权拒绝的 gRPC 调用应产生一条 deny 审计事件，旁路不阻断。
func TestGRPCAudit_Deny(t *testing.T) {
	if _, err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	repos = bootstrappkg.LastRepositories()
	a := &memAuditor{}
	tok := issueToken(t, "alice", "u-alice", "viewer")
	if err := invokeWithAudit(t, a, tok, "/go.bald.admin.v1.SecretService/DeleteSecret"); err == nil {
		t.Fatal("want PermissionDenied, got nil")
	}
	evs := a.all()
	if len(evs) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(evs))
	}
	if evs[0].Result != audit.ResultDeny {
		t.Errorf("result should be deny, got %q", evs[0].Result)
	}
	if evs[0].Action != "delete" {
		t.Errorf("action should be delete, got %q", evs[0].Action)
	}
}
