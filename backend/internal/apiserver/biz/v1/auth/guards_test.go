package auth

import (
	"testing"

	"github.com/kalandramo/bald/circuitbreaker/hystrix"
	"github.com/kalandramo/bald/ratelimit/tokenbucket"
	"github.com/kalandramo/bald/retry"
)

// TestLoginGuards_SettersAndReplace 钉住两套写入语义：
// SetLoginXxx 启动期逐项注入且 nil 不覆盖；ReplaceLoginGuards 是热更协调
// 入口，nil 表示「配置已移除 = 真的禁用」。
func TestLoginGuards_SettersAndReplace(t *testing.T) {
	b := New(nil, nil, nil, nil)

	lim, err := tokenbucket.New(1, 1)
	if err != nil {
		t.Fatalf("tokenbucket.New: %v", err)
	}
	b.SetLoginLimiter(lim)
	b.SetLoginBreaker(hystrix.New())
	b.SetLoginRetrier(retry.New())

	g := b.loginGuards.Load()
	if g == nil || g.limiter == nil || g.breaker == nil || g.retrier == nil {
		t.Fatalf("guards after setters = %+v, want all non-nil", g)
	}

	// 启动期语义：nil 不覆盖。
	b.SetLoginLimiter(nil)
	b.SetLoginBreaker(nil)
	b.SetLoginRetrier(nil)
	if g := b.loginGuards.Load(); g.limiter == nil || g.breaker == nil || g.retrier == nil {
		t.Fatalf("nil setter overwrote guards: %+v", g)
	}

	// 协调语义：nil = 真的关闭。
	b.ReplaceLoginGuards(nil, nil, nil)
	if g := b.loginGuards.Load(); g.limiter != nil || g.breaker != nil || g.retrier != nil {
		t.Fatalf("ReplaceLoginGuards(nil,nil,nil) = %+v, want all nil", g)
	}
}
