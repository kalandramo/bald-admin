package authz

// recording_test.go —— 判定记录装饰器的契约：判定结果零改变 + 写入 best-effort。
//
// 复用同包 alias_test.go 的 fakeAuthorizer（按「subject|object|action」集合判定，
// 记录调用序），不另造平行替身。

import (
	"context"
	"errors"
	"testing"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
)

// evalRecord 是一次 RecordEvaluation 调用的入参快照。
type evalRecord struct {
	tenantID, userID, permissionID string
	requestPath, requestMethod     string
	allowed                        bool
	details                        string
}

// fakeRecorder 是评估日志写入面替身：可注入 error / panic 故障。
type fakeRecorder struct {
	records   []evalRecord
	err       error
	panicWith any
}

func (f *fakeRecorder) RecordEvaluation(_ context.Context, tenantID, userID, permissionID,
	requestPath, requestMethod string, allowed bool, details string) error {
	if f.panicWith != nil {
		panic(f.panicWith)
	}
	f.records = append(f.records, evalRecord{
		tenantID: tenantID, userID: userID, permissionID: permissionID,
		requestPath: requestPath, requestMethod: requestMethod,
		allowed: allowed, details: details,
	})
	return f.err
}

func allowInner() *fakeAuthorizer {
	return &fakeAuthorizer{allow: map[string]bool{"u1|secret|get": true}}
}

// TestRecording_AllowRecordedWithFields 放行判定原样透传，且字段取自判定三元组。
func TestRecording_AllowRecordedWithFields(t *testing.T) {
	inner, rec := allowInner(), &fakeRecorder{}
	a := NewRecording(inner, func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
	if err != nil || !ok {
		t.Fatalf("Authorize=(%v,%v), want (true,nil)", ok, err)
	}
	if len(rec.records) != 1 {
		t.Fatalf("records=%d, want 1", len(rec.records))
	}
	got := rec.records[0]
	// permissionID/requestPath 同为 obj，requestMethod 为 act（ctx 无原始 path，
	// 见 recording.go 头注「字段取法」）。
	if got.userID != "u1" || got.permissionID != "secret" || got.requestPath != "secret" ||
		got.requestMethod != "get" || !got.allowed {
		t.Fatalf("记录字段不符: %+v", got)
	}
	if got.tenantID != "" || got.details != "" {
		t.Fatalf("无 claims 时租户/详情应为空: %+v", got)
	}
}

// TestRecording_DenyRecorded 拒绝同样落库（schema 的 Result bool 允许两种结论）。
func TestRecording_DenyRecorded(t *testing.T) {
	inner, rec := allowInner(), &fakeRecorder{}
	a := NewRecording(inner, func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "secret", "delete")
	if err != nil || ok {
		t.Fatalf("Authorize=(%v,%v), want (false,nil)", ok, err)
	}
	if len(rec.records) != 1 || rec.records[0].allowed {
		t.Fatalf("deny 未落库: %+v", rec.records)
	}
}

// TestRecording_ClaimsSupplyTenantAndRoles tenantID/详情取自 ctx 里的认证 claims
// （与 handler 的 tenantOf 同源）。
func TestRecording_ClaimsSupplyTenantAndRoles(t *testing.T) {
	inner, rec := allowInner(), &fakeRecorder{}
	a := NewRecording(inner, func() EvaluationRecorder { return rec })

	ctx := authn.ContextWithAuthClaims(context.Background(), &authn.AuthClaims{
		Subject: "u1", TenantID: "t-default", Roles: []string{"viewer", "ops"},
	})
	if _, err := a.Authorize(ctx, "u1", "secret", "get"); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	got := rec.records[0]
	if got.tenantID != "t-default" {
		t.Fatalf("tenantID=%q, want t-default", got.tenantID)
	}
	if got.details != "roles=viewer,ops" {
		t.Fatalf("details=%q, want roles=viewer,ops", got.details)
	}
}

// TestRecording_EngineErrorNotRecorded 引擎错误是 500 路径而非评估结论：
// 错误原样透传，不落库。
func TestRecording_EngineErrorNotRecorded(t *testing.T) {
	sentinel := errors.New("engine down")
	inner, rec := &fakeAuthorizer{err: sentinel}, &fakeRecorder{}
	a := NewRecording(inner, func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
	if !errors.Is(err, sentinel) || ok {
		t.Fatalf("Authorize=(%v,%v), want (false,%v)", ok, err, sentinel)
	}
	if len(rec.records) != 0 {
		t.Fatalf("引擎错误不应落库: %+v", rec.records)
	}
}

// TestRecording_StoreFailureKeepsDecision 写入失败（store 故障注入）不改变判定：
// allow 仍是 allow，deny 仍是 deny。
func TestRecording_StoreFailureKeepsDecision(t *testing.T) {
	for _, tc := range []struct {
		name, subject, object, action string
		want                          bool
	}{
		{"allow", "u1", "secret", "get", true},
		{"deny", "u1", "secret", "delete", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := allowInner()
			rec := &fakeRecorder{err: errors.New("unique constraint conflict")}
			a := NewRecording(inner, func() EvaluationRecorder { return rec })

			ok, err := a.Authorize(context.Background(), tc.subject, tc.object, tc.action)
			if err != nil {
				t.Fatalf("写入失败不应冒出错误: %v", err)
			}
			if ok != tc.want {
				t.Fatalf("判定被改写: got %v want %v", ok, tc.want)
			}
		})
	}
}

// TestRecording_StorePanicKeepsDecision 写入面 panic（如仓储聚合为 nil 触发空指针）
// 也必须被兜住——判定结果与错误都不受影响。
func TestRecording_StorePanicKeepsDecision(t *testing.T) {
	inner, rec := allowInner(), &fakeRecorder{panicWith: "nil store"}
	a := NewRecording(inner, func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
	if err != nil || !ok {
		t.Fatalf("Authorize=(%v,%v), want (true,nil)", ok, err)
	}
}

// TestRecording_NilRecorderPassthrough 未注入写入面（getter 为 nil，或 getter
// 返回 nil——装配层判空后返回 nil 接口）时纯透传，不 panic。
func TestRecording_NilRecorderPassthrough(t *testing.T) {
	inner := allowInner()
	variants := map[string]func() EvaluationRecorder{
		"nil getter": nil,
		"nil product": func() EvaluationRecorder {
			var rec *fakeRecorder // typed nil 经判空转成 nil 接口
			if rec == nil {
				return nil
			}
			return rec
		},
	}
	for name, getter := range variants {
		t.Run(name, func(t *testing.T) {
			a := NewRecording(inner, getter)
			ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
			if err != nil || !ok {
				t.Fatalf("Authorize=(%v,%v), want (true,nil)", ok, err)
			}
		})
	}
}

// TestRecording_NilInnerFailClosed 无内层授权器：fail-closed，且**不落库**——
// 根本没有引擎参与，这不是一次策略评估结论（与引擎错误不记同理）。
func TestRecording_NilInnerFailClosed(t *testing.T) {
	rec := &fakeRecorder{}
	a := NewRecording(nil, func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
	if err != nil || ok {
		t.Fatalf("Authorize=(%v,%v), want (false,nil)", ok, err)
	}
	if len(rec.records) != 0 {
		t.Fatalf("无内层不应落库: %+v", rec.records)
	}
}

// TestRecording_AliasRetryYieldsOneRow 装饰器装在最外层时，别名重试（内层两次
// Authorize）只产生一条日志——一次请求一次评估。
func TestRecording_AliasRetryYieldsOneRow(t *testing.T) {
	inner := &fakeAuthorizer{allow: map[string]bool{"u1|org-units|list": true}}
	rec := &fakeRecorder{}
	a := NewRecording(NewObjectAlias(inner, map[string]string{"orgunit": "org-units"}),
		func() EvaluationRecorder { return rec })

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if err != nil || !ok {
		t.Fatalf("Authorize=(%v,%v), want (true,nil)", ok, err)
	}
	if len(inner.calls) != 2 {
		t.Fatalf("内层应调用两次（原名 + 别名）: %v", inner.calls)
	}
	if len(rec.records) != 1 {
		t.Fatalf("一次请求应只落一条日志: %+v", rec.records)
	}
	if rec.records[0].permissionID != "orgunit" {
		t.Fatalf("记录的是请求原 object（非别名）: %+v", rec.records[0])
	}
}

// TestRecording_ImplementsAuthorizer 编译期契约：装饰器是合法 Authorizer。
func TestRecording_ImplementsAuthorizer(t *testing.T) {
	var _ authz.Authorizer = NewRecording(nil, nil)
}
