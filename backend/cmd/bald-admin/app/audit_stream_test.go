package app

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/audit"
	goredis "github.com/redis/go-redis/v9"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// audit_stream_test.go —— 契约轨的 stream 后端注册回归。
//
// ## 背景
//
// 审计装配有两条轨：
//   - **契约轨**（audit_wiring.go 的 auditRegistry）：audit 段经
//     WithAuditRegistry 在阶段 B 装配；
//   - **协调轨**（audit.go 的 reconcileAudit）：audit.backends 期望态经 R1-2
//     协调器在 afterStart 后收敛。
//
// 协调轨的 stream 分支早已就位（buildAuditBackend 的 case "stream"），但
// **契约轨原先只注册了 store**——配置 `backends: "store,stream"` 时 Build
// 会 fail-fast（`audit provider "stream" not registered`），与协调轨能力不一致。
//
// ## 本测试锁定
//
// auditRegistry 注册了 stream 后端；含 stream 的 backends 列表能装配成功，
// 且事件真实发布到 Redis Stream（miniredis）。

// TestAuditRegistry_StreamProviderRegistered —— 契约轨已注册 stream 后端。
//
// RED（修复前）：auditRegistry() 只注册 store，本测试的 Build 会因
// 「audit provider "stream" not registered」失败。
func TestAuditRegistry_StreamProviderRegistered(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 模拟阶段 B 后的桥接状态：RedisClient 已由 buildCaches 填好。
	origClient := bootstrappkg.RedisClient
	bootstrappkg.RedisClient = rdb
	t.Cleanup(func() { bootstrappkg.RedisClient = origClient })

	reg := auditRegistry()
	cfg := &bootstrapv1.Audit{
		Backends: []string{"store", "stream"},
		Stream:   &bootstrapv1.Audit_Stream{Stream: "audit.test", Buffer: 16},
	}

	// res 传 nil：store 后端在无 DB 时 fail-open（NewStore(nil)），
	// stream 后端从包级 RedisClient 取连接——两者都不依赖 res。
	a, cleanup, err := reg.Build(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Build with stream backend failed: %v\n"+
			"（契约轨未注册 stream provider？错误应直指缺失后端）", err)
	}
	if a == nil {
		t.Fatal("Build returned nil auditor")
	}
	if cleanup != nil {
		t.Cleanup(func() { _ = cleanup(context.Background()) })
	}

	// 行为断言：事件真的进 Redis Stream（不只是「不报错」）。
	a.Record(context.Background(), streamProbeEvent())
	waitStreamLen(t, rdb, "audit.test", 1)
}

// streamProbeEvent 构造一条可识别的审计事件。
func streamProbeEvent() audit.AuditEvent {
	return audit.AuditEvent{
		Subject: "u-stream-probe",
		Object:  "stream",
		Action:  "probe",
		Result:  audit.ResultAllow,
	}
}

// waitStreamLen 等待 Redis Stream 长度达到 want（后台 goroutine 异步发布）。
func waitStreamLen(t *testing.T, rdb *goredis.Client, stream string, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last int64
	for time.Now().Before(deadline) {
		n, err := rdb.XLen(context.Background(), stream).Result()
		if err == nil {
			last = n
			if n >= want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stream %q length = %d, want >= %d（事件未发布到 Redis Stream）", stream, last, want)
}

// 保证 appkit 被引用（Build 签名含 *AppKit）。
var _ = (*appkit.AppKit)(nil)
