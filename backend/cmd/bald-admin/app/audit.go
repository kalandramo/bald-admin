package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"

	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	auditstream "github.com/kalandramo/bald/contrib/audit-stream"
	baldlog "github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/audit"
)

var reconAuditors = struct {
	mu  sync.Mutex
	set map[string]audit.Auditor
}{set: make(map[string]audit.Auditor)}

// applyAuditors 根据当前后端表重建全局 MultiAuditor（按后端名排序，顺序确定、
// 便于测试与排查；空则退化为 Nop，绝不阻断审计旁路）。

func applyAuditors() {
	reconAuditors.mu.Lock()
	names := make([]string, 0, len(reconAuditors.set))
	for n := range reconAuditors.set {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]audit.Auditor, 0, len(names))
	for _, n := range names {
		list = append(list, reconAuditors.set[n])
	}
	reconAuditors.mu.Unlock()
	if len(list) == 0 {
		audit.SetAuditor(audit.NopAuditor())
		return
	}
	audit.SetAuditor(securityaudit.NewMulti(list...))
}

// auditBackendComponent 是一个后端审计器组件：Mount 即把自身审计器注册进全局
// MultiAuditor，Unmount 即注销并收尾后端自身生命周期。其名 == 后端名
// （log/store/stream），正是协调器用于 diff 的标识，与 ReconcileCtx.Mounted() 一一对应。

type auditBackendComponent struct {
	name  string
	build func() audit.Auditor // 延迟构造：依赖 InitBridges 后的 DB/Redis
	aud   audit.Auditor        // Start 期构造的实例，Dispose 时按需收尾
}

func (c *auditBackendComponent) Name() string { return c.name }

func (c *auditBackendComponent) Start(ctx context.Context) error {
	a := c.build()
	if a == nil {
		return fmt.Errorf("audit backend %q unavailable", c.name)
	}
	c.aud = a
	reconAuditors.mu.Lock()
	reconAuditors.set[c.name] = a
	reconAuditors.mu.Unlock()
	applyAuditors()
	baldlog.Info(ctx, "audit backend mounted", "backend", c.name)
	return nil
}

func (c *auditBackendComponent) Dispose(ctx context.Context) error {
	reconAuditors.mu.Lock()
	delete(reconAuditors.set, c.name)
	reconAuditors.mu.Unlock()
	applyAuditors()
	// 后端自身生命周期收尾：StreamAuditor 停后台 goroutine 并 drain 剩余缓冲
	// （Mount/Unmount 由 ReconcileCtx 串行化，无并发写 c.aud；log/store 无
	// Close 方法则零副作用）。否则 audit.backends 热切换每次泄漏一个 goroutine
	// + 1024 容量 chan，且已入队事件被静默丢弃。
	if c.aud != nil {
		if closer, ok := c.aud.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	baldlog.Info(ctx, "audit backend unmounted", "backend", c.name)
	return nil
}

// buildAuditBackend 构造某后端的审计器（log 始终可用；store/stream 依赖桥接就绪）。
// 桥接未就绪时返回 nil，协调器会跳过该后端（旁路语义，绝不阻断启动）。
//
// streamName/streamBuf 是 stream 后端参数（取自协调器配置快照
// audit.stream.stream / audit.stream.buffer）。空值/0 走实现缺省
// （"audit.events" / 1024）——此前忽略该配置会导致声明被静默丢弃。
func buildAuditBackend(name, streamName string, streamBuf int) audit.Auditor {
	switch name {
	case "log":
		return securityaudit.New()
	case "store":
		if bootstrappkg.DB != nil {
			return securityaudit.NewStore(bootstrappkg.DB)
		}
	case "stream":
		// D1：底层 client 经 bootstrap.RedisClient 复用（cache/redis 适配器
		// 不暴露 client）；nil 即无 Redis，跳过该后端。
		if bootstrappkg.RedisClient != nil {
			// stream/buffer 取自协调器配置快照。空串/0 由实现兜底
			// （WithStream 对空串保持缺省 "audit.events"、New 对
			// buffer<=0 回落到 1024），故直接传即可。
			return securityaudit.NewStream(bootstrappkg.RedisClient,
				auditstream.WithStream(streamName),
				auditstream.WithBuffer(streamBuf),
			)
		}
	}
	return nil
}

// reconcileAudit 是 R1-2 期望态协调函数（逐后端粒度，非整体重建）：配置键
// audit.backends 声明「期望的审计后端集合」，框架用 ReconcileCtx 暴露的实际态
// （r.Mounted()，即本协调器已挂载的后端名）做 diff，仅 Mount 新增、Unmount 移除——
// 完整兑现「只更新变动部分」的 K8s controller 语义。部分失败不回滚，下次协调补齐。
//
// 触发时机由框架负责：首次在 loadConfig 基线后、AfterStart 前的启动收敛期；其后
// 每次 OnConfigChange 携带新快照再次调用，实现运行期热切换（如 [store]→[store,stream]）。

func reconcileAudit(ctx context.Context, rctx *appkit.ReconcileCtx) error {
	want := parseAuditBackends(rctx.String("audit.backends"))
	have := rctx.Mounted() // 实际态：本协调器名下已挂载的后端（Mount/Unmount 维护）

	// diff-apply：期望==实际时直接返回（幂等，不抖动、不发多余审计）。
	add, remove := appkit.DiffStrings(want, have)
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}

	baldlog.Info(ctx, "reconcile audit.backends",
		"add", strings.Join(add, ","), "remove", strings.Join(remove, ","))

	// 新增：期望有、实际无 → 逐后端 Mount（底层 A1 组件生命周期 + 重组审计）。
	// stream 后端参数取自协调器的配置快照（rctx 读 audit.stream.* 键）——
	// 与期望态同源，热切换时新参数随之生效。
	streamName := rctx.String("audit.stream.stream")
	streamBuf := 0
	if v := rctx.String("audit.stream.buffer"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			streamBuf = n
		}
	}
	for _, name := range add {
		comp := &auditBackendComponent{name: name, build: func() audit.Auditor {
			return buildAuditBackend(name, streamName, streamBuf)
		}}
		if err := rctx.Mount(ctx, comp.Name(), comp); err != nil {
			// 失败不回滚，下次协调按实际态（reconItems）重新 diff 补齐。
			baldlog.Error(ctx, "reconcile mount audit backend failed",
				"backend", name, "error", err)
		}
	}
	// 移除：实际有、期望无 → 逐后端 Unmount。
	for _, name := range remove {
		if err := rctx.Unmount(ctx, name); err != nil {
			baldlog.Error(ctx, "reconcile unmount audit backend failed",
				"backend", name, "error", err)
		}
	}
	return nil
}

// parseAuditBackends 解析逗号/空格分隔的后端列表，过滤非法取值，去重保序。

func parseAuditBackends(s string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		b := strings.TrimSpace(raw)
		if b == "" || (b != "log" && b != "store" && b != "stream") {
			continue
		}
		if _, ok := seen[b]; ok {
			continue
		}
		seen[b] = struct{}{}
		out = append(out, b)
	}
	return out
}

// contains 判断切片是否含某元素。

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// appRefT 是 AppKit 的线程安全迟到绑定句柄：管理面路由在 appkit.New 之前注册
// （gin 装配先于 app 构造），handler 闭包捕获 appRef、请求期读取最新 App 实例。
