// assembly_biz.go —— 装配层 Redis 适配余件。
//
// 各域 biz 由各自域声明包的 Wire 构造（internal/apiserver/domains/<d>d，经 Deps
// getter 迟到取用装配层抉择的运行期产物）。本文件只余装配层自持的 Redis env 兼容
// 通道（secret/dict 域 Cache-Aside 兜底），仍是「普通 Go 函数显式装配」形态：
//   - provideRedisAddr：BALD_ADMIN_REDIS_ADDR env 通道（CI 覆盖手段）；
//   - newRedisCache：适配 bootstrap.BuildRedisCache（cache/redis 适配器；
//     保留错误，Redis 不可达即启动失败），addr 为空返回 nil（禁用态）。
package app

import (
	"os"

	"github.com/kalandramo/bald-admin/internal/bootstrap"
	"github.com/kalandramo/bald/cache"
)

// redisAddr 是命名类型别名，区分 string 依赖（避免同名同型参数传错）。
type redisAddr string

// provideRedisAddr 从 env 提供 Redis 地址（空=禁用缓存，直连 store）。
// 完整 Redis 参数（addr/password/db）经 bootstrap.Configure 由配置段注入，
// 此处仅保留 secret 缓存的 env 兼容通道。
func provideRedisAddr() redisAddr { return redisAddr(os.Getenv("BALD_ADMIN_REDIS_ADDR")) }

// newRedisCache 适配 redisAddr→bootstrap.BuildRedisCache（cache/redis 适配器；
// 保留错误，Redis 不可达即启动失败）。addr 为空返回 nil（禁用态）。
func newRedisCache(addr redisAddr) (cache.Cache, error) {
	return bootstrap.BuildRedisCache(string(addr), "", 0)
}
