// Package cachemonitor 是 Redis 缓存监控业务（Wave 6.2，自 go-wind-admin
// redis_cache 域精简移植，1 rpc）。
//
// ## fail-soft 语义（对齐源 redis_cache_monitor_repo.go）
//
// 三个只读命令（INFO / DBSIZE / SLOWLOG GET）**任一失败不阻断其余**：失败项
// 置空并记日志，而非整体报错。Redis 不可用（RedisClient 为 nil）时返回**空视图**
// （非错误）——避免单点故障导致整个监控页不可用。
//
// ## UTF-8 净化（源实测踩坑）
//
// Redis 命令参数是二进制安全的（如二进制序列化的 key），而 proto string 字段
// **强制 UTF-8**——未净化直接透传会让响应在 HTTP codec 序列化阶段整体失败
// （500 "invalid UTF-8"）。故 slowlog 的 args/client_name 经 strings.ToValidUTF8
// 净化（对齐源 sanitizeSlowLogArgs）。
package cachemonitor

import (
	"context"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/kalandramo/bald/log"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// slowLogFetchLimit SLOWLOG GET 抓取的最近条目数上限（与源一致）。
const slowLogFetchLimit = 10

// Biz 缓存监控业务。
type Biz struct{}

// New 构造缓存监控业务。
func New() *Biz { return &Biz{} }

// InfoSection INFO 输出的单个 section。
type InfoSection struct {
	Name    string
	Entries []InfoEntry
}

// InfoEntry INFO 输出的单个 key/value 对。
type InfoEntry struct {
	Key   string
	Value string
}

// SlowLogEntry SLOWLOG GET 返回的单条慢日志。
type SlowLogEntry struct {
	ID           int64
	CreatedAt    time.Time
	DurationUsec int64
	Args         []string
	ClientAddr   string
	ClientName   string
}

// Info 聚合 INFO / DBSIZE / SLOWLOG 三类只读运维指标。
//
// 任一命令失败仅记日志并将对应字段置空，不阻断其余（fail-soft）。Redis 不可用
// 返回空视图（非错误）。
func (b *Biz) Info(ctx context.Context) (*Result, error) {
	out := &Result{}
	rdb := bootstrappkg.RedisClient
	if rdb == nil {
		return out, nil // Redis 不可用 → 空视图（对齐源）
	}

	if raw, err := rdb.Info(ctx).Result(); err != nil {
		log.Warn(ctx, "redis INFO failed", "error", err.Error())
	} else {
		out.Sections = parseInfoSections(raw)
	}

	if n, err := rdb.DBSize(ctx).Result(); err != nil {
		log.Warn(ctx, "redis DBSIZE failed", "error", err.Error())
	} else if n >= 0 {
		out.DBSize = uint64(n)
	}

	if entries, err := rdb.SlowLogGet(ctx, slowLogFetchLimit).Result(); err != nil {
		log.Warn(ctx, "redis SLOWLOG GET failed", "error", err.Error())
	} else {
		out.Slowlog = mapSlowLogEntries(entries)
	}

	return out, nil
}

// Result 是聚合结果。
type Result struct {
	Sections []InfoSection
	DBSize   uint64
	Slowlog  []SlowLogEntry
}

// parseInfoSections 解析 Redis INFO 原始串为 section/entry 树。
//
// INFO 输出格式：`# <SectionName>` 行标记新 section，其后 `key:value` 行为条目；
// 空行与无法识别的行被忽略。Keyspace section 的 `db0:keys=...` 同样按 key/value
// 落入（前端按通用 kv 表渲染）。
func parseInfoSections(raw string) []InfoSection {
	var sections []InfoSection
	cur := -1
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			name := strings.TrimSpace(line[2:])
			if name == "" {
				cur = -1 // 畸形 section 头：停止挂条目，避免误归属
				continue
			}
			sections = append(sections, InfoSection{Name: name})
			cur = len(sections) - 1
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 || cur < 0 {
			continue // 无归属 section 的孤立条目，忽略
		}
		sections[cur].Entries = append(sections[cur].Entries, InfoEntry{
			Key:   line[:idx],
			Value: line[idx+1:],
		})
	}
	return sections
}

// mapSlowLogEntries 映射 go-redis 的 SlowLog 列表（含 UTF-8 净化）。
func mapSlowLogEntries(in []goredis.SlowLog) []SlowLogEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]SlowLogEntry, 0, len(in))
	for i := range in {
		s := &in[i]
		out = append(out, SlowLogEntry{
			ID:           s.ID,
			CreatedAt:    s.Time,
			DurationUsec: s.Duration.Microseconds(),
			Args:         sanitizeUTF8(s.Args),
			ClientAddr:   s.ClientAddr,
			ClientName:   strings.ToValidUTF8(s.ClientName, "\uFFFD"),
		})
	}
	return out
}

// sanitizeUTF8 把命令参数净化为合法 UTF-8（proto string 字段强制 UTF-8）。
func sanitizeUTF8(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, strings.ToValidUTF8(a, "\uFFFD"))
	}
	return out
}
