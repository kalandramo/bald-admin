package e2e

// redis_test_helper.go —— e2e 的 Redis 连接参数集中解析（2026-09-22）。
//
// ## 为什么需要它
//
// 此前 5 个 e2e 文件（t_w1d_auth / t_w1d4_token / t_w1d_captcha /
// t_w1_5_mfa / t_w2_6_broker）各自**硬编码** `127.0.0.1:6379`，导致：
//   - 环境里的 Redis 若不是本地 6379（如集群内的远端实例），这批测试
//     一律 `t.Skip("redis 不可达")`——**验证能力被环境锁死**；
//   - 想在真实 Redis 上验证（如 D6 修复后的 refresh token 唯一性）
//     只能临时改代码，改完又得改回。
//
// 本文件把连接参数收敛为一处，并支持环境变量覆盖——**与仓库既有惯例一致**
// （`t_w4_4_registry_e2e_test.go` 的 `BALD_E2E_ETCD_ADDR`、
// `t5_file_e2e_test.go` 的 `BALD_ADMIN_TEST_MINIO_*`）。
//
// ## 用法
//
//	export BALD_E2E_REDIS_ADDR=10.82.138.249:30967
//	export BALD_E2E_REDIS_PASSWORD=***
//	go test ./internal/apiserver/e2e/ -run TestWave1d
//
// 不设环境变量时回落本地 `127.0.0.1:6379`（docker 起的实例）——**既有行为
// 零改动**：本地无 Redis 时仍按约定 Skip，不会伪装通过。

import (
	"os"

	goredis "github.com/redis/go-redis/v9"
)

// defaultTestRedisAddr 是无环境变量时的回落地址（本地 docker 实例）。
const defaultTestRedisAddr = "127.0.0.1:6379"

// redisTestAddr 返回 e2e 使用的 Redis 地址。
//
// 优先级：`BALD_E2E_REDIS_ADDR` > 本地默认。返回裸 host:port（不含 scheme）——
// 供 goredis.Options.Addr 使用；broker 场景需 `redis://` 前缀时自行拼接
// （见 redisTestURL）。
func redisTestAddr() string {
	if v := os.Getenv("BALD_E2E_REDIS_ADDR"); v != "" {
		return v
	}
	return defaultTestRedisAddr
}

// redisTestPassword 返回 e2e 使用的 Redis 密码（无则空串 = 免密）。
//
// 远端/受管 Redis 通常强制鉴权（实测裸 PING 返回 `NOAUTH Authentication
// required`），故必须支持密码注入，否则连上也无法认证。
func redisTestPassword() string {
	return os.Getenv("BALD_E2E_REDIS_PASSWORD")
}

// redisTestURL 返回带 `redis://` scheme 的连接 URL（broker 场景用）。
//
// 形如 `redis://:password@host:port`；无密码时 `redis://host:port`。
// broker 的 DialURL 要求 scheme，而 goredis 的 Options.Addr 要求裸地址——
// 两者需求不同，故分两个函数。
func redisTestURL() string {
	addr := redisTestAddr()
	pw := redisTestPassword()
	if pw == "" {
		return "redis://" + addr
	}
	return "redis://:" + pw + "@" + addr
}

// redisTestOptions 构造指向目标 Redis 的 goredis.Options（指定 DB 隔离）。
//
// 各测试用不同 DB 号隔离（12-15），避免相互污染——保持既有约定不变。
func redisTestOptions(db int) *goredis.Options {
	return &goredis.Options{
		Addr:     redisTestAddr(),
		Password: redisTestPassword(),
		DB:       db,
	}
}
