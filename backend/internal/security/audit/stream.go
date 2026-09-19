package audit

import (
	"github.com/redis/go-redis/v9"

	"github.com/kalandramo/bald/contrib/audit-stream"
	"github.com/kalandramo/bald/pkg/audit"
)

// NewStream 构造 Redis Stream 审计后端（转发 contrib/audit-stream）。
// rdb 为 nil 返回 nil（调用方跳过；typed-nil 接口陷阱在此拦截）。
// 生命周期收尾用 Close() error（停后台 goroutine + drain 尾批缓冲），
// 签名适配 appkit.ComponentFunc。
//
// D1：入参类型为 redis.UniversalClient（与 cache/redis 适配器、contrib/audit-stream
// 的 New 一致）——原 *redis.Client 是旧 cache-redis 的 Client() 返回类型，已随该
// 组件删除而调整。
func NewStream(rdb redis.UniversalClient) *auditstream.StreamAuditor {
	if rdb == nil {
		return nil
	}
	return auditstream.New(rdb)
}

// NewMulti 构造组合审计后端（转发框架核心 MultiAuditor；空切片返回 nop）。
func NewMulti(auditors ...audit.Auditor) audit.Auditor { return audit.NewMultiAuditor(auditors...) }
