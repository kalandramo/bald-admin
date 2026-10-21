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
// 入参类型为 redis.UniversalClient（与 cache/redis 适配器、contrib/audit-stream
// 的 New 一致）。
//
// opts 透传给 auditstream.New（如 WithStream/WithBuffer）——调用方若需按契约段
// 定制 stream 名/缓冲，传对应 Option；不传走实现缺省（"audit.events" / 1024）。
func NewStream(rdb redis.UniversalClient, opts ...auditstream.Option) *auditstream.StreamAuditor {
	if rdb == nil {
		return nil
	}
	return auditstream.New(rdb, opts...)
}

// NewMulti 构造组合审计后端（转发框架核心 MultiAuditor；空切片返回 nop）。
func NewMulti(auditors ...audit.Auditor) audit.Auditor { return audit.NewMultiAuditor(auditors...) }
