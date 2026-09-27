package app

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver"

	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/appkit"
)

type appRefT struct {
	mu  sync.RWMutex
	app *appkit.AppKit
}

func (r *appRefT) set(a *appkit.AppKit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.app = a
}

func (r *appRefT) get() *appkit.AppKit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.app
}

// heartbeatComp 是 M10.2 的演示组件：Start 起周期心跳 goroutine，Dispose 停止并
// 等待退出——与 StreamAuditor 同构的「有 goroutine 要收」组件生命周期范本，
// 经管理面挂载/卸载可端到端观察 A1 的 Start/Dispose 与重组审计。

type heartbeatComp struct {
	name  string
	stop  chan struct{}
	done  chan struct{}
	appFn func() *appkit.AppKit
}

func newHeartbeatComponent(appFn func() *appkit.AppKit) appkit.Component {
	return &heartbeatComp{
		name:  "demo.heartbeat",
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		appFn: appFn,
	}
}

func (h *heartbeatComp) Name() string { return h.name }

func (h *heartbeatComp) Start(_ context.Context) error {
	go func() {
		defer close(h.done)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				baldlog.Info(context.Background(), "component heartbeat",
					"component", h.name, "components", len(h.appFn().ListComponents()))
			}
		}
	}()
	return nil
}

func (h *heartbeatComp) Dispose(_ context.Context) error {
	close(h.stop)
	<-h.done // 等 goroutine 退出：Dispose 后组件对进程无残留影响（时间可组合性）
	return nil
}

// registerAdminRoutes 把管理面路由挂到 router（appRef 迟到绑定见 appRefT）。

func registerAdminRoutes(router *gin.Engine, ref *appRefT, factories map[string]apiserver.ComponentFactory) {
	apiserver.RegisterAdmin(router, ref.get, factories)
}
