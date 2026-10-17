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

// audit_stream_test.go —— 契约轨 audit Provider 的 stream 后端回归。
//
// ## 背景
//
// 审计装配有两条轨：
//   - **契约轨**（audit_wiring.go 的 auditProvider）：audit 段在阶段 B 经
//     段驱动 Provider 装配；
//   - **协调轨**（audit.go 的 reconcileAudit）：audit.backends 期望态经 R1-2
//     协调器在 AfterStart 前收敛。
//
// 协调轨的 stream 分支早已就位（buildAuditBackend 的 case "stream"），但
// **契约轨原先只注册了 store**——配置 `backends: "store,stream"` 时装配会
// fail-fast，与协调轨能力不一致。
//
// ## 本测试锁定
//
// auditProvider 接管全部三后端（末者胜覆盖 std 的 log-only）；含 stream 的
// backends 列表能装配成功，且事件真实发布到 Redis Stream（miniredis）。

// TestAuditProvider_StreamBackendAssembled —— 契约轨含 stream 后端可装配。
//
// RED（修复前）：契约轨只装配 store，本测试的 Build 会因未知后端 "stream" 失败。
func TestAuditProvider_StreamBackendAssembled(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 模拟阶段 B 后的桥接状态：RedisClient 已由 cacheRedisProvider 填好。
	origClient := bootstrappkg.RedisClient
	bootstrappkg.RedisClient = rdb
	t.Cleanup(func() { bootstrappkg.RedisClient = origClient })

	cfg := &bootstrapv1.BootstrapConfig{
		Audit: &bootstrapv1.Audit{
			Backends: []string{"store", "stream"},
			Stream:   &bootstrapv1.Audit_Stream{Stream: "audit.test", Buffer: 16},
		},
	}

	// App 传 nil：store 后端在无 DB 时 fail-open（NewStore(nil)），
	// stream 后端从包级 RedisClient 取连接——两者都不依赖资源面。
	p := auditProvider{}
	prod, cleanup, err := p.Build(context.Background(), appkit.BuildScope{Config: cfg})
	if err != nil {
		t.Fatalf("Build with stream backend failed: %v\n"+
			"（契约轨未接管 stream 后端？错误应直指缺失后端）", err)
	}
	if prod == nil {
		t.Fatal("Build returned nil auditor")
	}
	if cleanup != nil {
		t.Cleanup(func() { _ = cleanup(context.Background()) })
	}

	// 行为断言：事件真的进 Redis Stream（不只是「不报错」）。
	prod.(audit.Auditor).Record(context.Background(), streamProbeEvent())
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

// 保证 appkit 被引用（BuildScope 含 *App 字段）。
var _ = (*appkit.App)(nil)
