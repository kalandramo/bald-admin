package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/kalandramo/bald/pkg/authz"
)

// fakeAuthorizer 记录被调用的 (object, action) 序列，并按预设集合判定。
// 用于验证别名装饰器「先原样、再别名」的调用次序与结果传播。
type fakeAuthorizer struct {
	// allow 是「subject|object|action」的放行集合。
	allow map[string]bool
	// calls 记录每次 Authorize 的 object 参数（按调用序）。
	calls []string
	// err 非 nil 时直接返回该错误（模拟引擎故障）。
	err error
}

func (f *fakeAuthorizer) Authorize(_ context.Context, subject, object, action string) (bool, error) {
	f.calls = append(f.calls, object)
	if f.err != nil {
		return false, f.err
	}
	return f.allow[subject+"|"+object+"|"+action], nil
}

// TestObjectAlias_OriginalAllowed 内层直接放行时不查别名，且只调用一次。
func TestObjectAlias_OriginalAllowed(t *testing.T) {
	inner := &fakeAuthorizer{allow: map[string]bool{"u1|org-units|list": true}}
	a := NewObjectAlias(inner, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "org-units", "list")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("want allowed=true for original object")
	}
	if len(inner.calls) != 1 || inner.calls[0] != "org-units" {
		t.Fatalf("calls=%v, want exactly one call with original object", inner.calls)
	}
}

// TestObjectAlias_AliasHit 原样被拒、别名放行 —— 这正是 gRPC 面 org 的场景。
func TestObjectAlias_AliasHit(t *testing.T) {
	// 策略里是 org-units；gRPC 归一化产出 orgunit。
	inner := &fakeAuthorizer{allow: map[string]bool{"u1|org-units|list": true}}
	a := NewObjectAlias(inner, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("want allowed=true via alias orgunit->org-units")
	}
	want := []string{"orgunit", "org-units"}
	if len(inner.calls) != 2 || inner.calls[0] != want[0] || inner.calls[1] != want[1] {
		t.Fatalf("calls=%v, want %v (original then alias)", inner.calls, want)
	}
}

// TestObjectAlias_AliasMiss 原样与别名均被拒 —— 不放宽语义边界。
func TestObjectAlias_AliasMiss(t *testing.T) {
	inner := &fakeAuthorizer{allow: map[string]bool{}}
	a := NewObjectAlias(inner, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if ok {
		t.Fatal("want allowed=false when both original and alias are denied")
	}
}

// TestObjectAlias_NoAliasEntry 别名表无此键时只调用一次，行为等同内层。
func TestObjectAlias_NoAliasEntry(t *testing.T) {
	inner := &fakeAuthorizer{allow: map[string]bool{}}
	a := NewObjectAlias(inner, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "secret", "get")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if ok {
		t.Fatal("want allowed=false")
	}
	if len(inner.calls) != 1 {
		t.Fatalf("calls=%v, want 1 (no alias retry for unmapped object)", inner.calls)
	}
}

// TestObjectAlias_InnerErrorPropagated 内层报错必须透传，不得被别名重试吞掉。
func TestObjectAlias_InnerErrorPropagated(t *testing.T) {
	sentinel := errors.New("casbin: enforce failed")
	inner := &fakeAuthorizer{err: sentinel}
	a := NewObjectAlias(inner, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want sentinel propagated (engine error must not be masked)", err)
	}
	if ok {
		t.Fatal("want allowed=false on error")
	}
	if len(inner.calls) != 1 {
		t.Fatalf("calls=%v, want 1 (no retry after engine error)", inner.calls)
	}
}

// TestObjectAlias_NilInner 内层为 nil 时 fail-closed（拒绝，不 panic）。
func TestObjectAlias_NilInner(t *testing.T) {
	a := NewObjectAlias(nil, map[string]string{"orgunit": "org-units"})

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if ok {
		t.Fatal("want fail-closed (allowed=false) when inner is nil")
	}
}

// TestObjectAlias_EmptyTable 空别名表：等价于直接委托内层。
func TestObjectAlias_EmptyTable(t *testing.T) {
	inner := &fakeAuthorizer{allow: map[string]bool{"u1|orgunit|list": true}}
	a := NewObjectAlias(inner, nil)

	ok, err := a.Authorize(context.Background(), "u1", "orgunit", "list")
	if err != nil {
		t.Fatalf("Authorize: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("want allowed=true (empty table delegates to inner)")
	}
}

// TestObjectAlias_ImplementsAuthorizer 编译期契约：装饰器是合法 Authorizer。
func TestObjectAlias_ImplementsAuthorizer(t *testing.T) {
	var _ authz.Authorizer = NewObjectAlias(nil, nil)
}
