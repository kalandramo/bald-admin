// Package cachekit 提供 Cache-Aside 的键构造工具（纯函数，零依赖）。
//
// 抽出原因（Wave 3）：这些函数原在 internal/bootstrap/cacheaside.go，但它们是
// **纯字符串工具**、无任何状态——留在 bootstrap 会让 biz 层为了拼一个缓存键而
// 反向依赖装配根（违反 G2「控制面单向依赖数据面」）。迁到本包后，biz 只依赖
// 纯函数，装配根与 biz 之间不再有这条隐式边。
//
// 键格式与 bootstrap 时期的实现逐字节一致（迁移零行为变更）。
package cachekit

import (
	"fmt"
	"strings"
	"time"
)

// DefaultCacheTTL 是 Cache-Aside 回填的默认 TTL（对齐旧 rediscache 的 5 分钟）。
const DefaultCacheTTL = 5 * time.Minute

// CacheKey 构造缓存键：各段以 ':' 连接。调用方须把租户维度放入键中，
// 防止跨租户缓存泄漏（与旧 rediscache.Key 逐字节等价）：
//
//	CacheKey("secret", tenant, id) // -> "secret:t-default:s-1"
func CacheKey(parts ...string) string {
	return strings.Join(parts, ":")
}

// CacheKeyPrefix 返回参数化键的「已知前缀（含尾冒号）」，供 loadable 的 loader
// 反解业务参数。例：CacheKeyPrefix("secret", tenant) -> "secret:t-default:"。
func CacheKeyPrefix(parts ...string) string {
	return CacheKey(parts...) + ":"
}

// CutCacheKeyPrefix 从 key 中裁掉 prefix，返回其后的业务键段。
// prefix 不匹配时返回错误——缓存键格式与 loader 前缀不一致属装配错误，
// 必须响亮失败而非静默返回错数据。
func CutCacheKeyPrefix(key, prefix string) (string, error) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", fmt.Errorf("cache key %q does not carry expected prefix %q", key, prefix)
	}
	return rest, nil
}
