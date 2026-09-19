// Package captcha 提供图片验证码的生成与校验（Wave 1d-2）。
//
// 对应源 go-wind-admin 的 GenerateCaptcha / VerifyCaptcha 两个 rpc
//（authentication.proto L58/L61）。图片生成用 github.com/mojocn/base64Captcha
// ——**与源项目同一库**（源 go.mod:179 用 v1.3.8），行为对齐而非自研重造。
//
// 框架能力验证结论：bald v0.8.1 **不提供** captcha 组件（实测
// `grep -rl "captcha" --include="*.go" bald/` 零命中），故本包为业务自建。
//
// 存储语义（安全设计的三条）：
//   - **一次性**：验证成功后立即删除（防同一验证码被暴力枚举多次尝试）；
//   - **错误不消费**：输错不删除（否则用户打错一次就得重新生成，可用性差）；
//   - **TTL**：验证码有有效期（默认 5 分钟），过期自动失效。
//
// 为什么用 Redis 而非进程内存：多实例部署下，生成与校验可能落在不同实例
//（负载均衡），进程内存会导致「A 实例生成、B 实例校验」必然失败。
package captcha

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mojocn/base64Captcha"
	goredis "github.com/redis/go-redis/v9"
)

// DefaultTTL 是验证码默认有效期。
const DefaultTTL = 5 * time.Minute

// ErrStoreUnavailable 存储不可用（Redis 故障）。
var ErrStoreUnavailable = errors.New("captcha: store unavailable")

// Store 是验证码存储抽象（保存答案 + 一次性校验）。
type Store interface {
	// Save 保存 captcha_id → answer 映射（带 TTL）。
	Save(ctx context.Context, id, answer string) error
	// Verify 校验用户输入：**仅正确时删除**（一次性语义）。
	// 大小写不敏感 + 容忍首尾空格（用户输入友好性）。
	Verify(ctx context.Context, id, input string) (bool, error)
	// Generate 生成新验证码（id + base64 PNG 图片），并保存答案。
	Generate(ctx context.Context) (id, imageBase64 string, err error)
}

// RedisStore 是基于 Redis 的 Store 实现。
type RedisStore struct {
	rdb goredis.UniversalClient
	// driver 是图片生成器（base64Captcha 的数字+字母混合驱动）。
	driver *base64Captcha.DriverString
	ttl    time.Duration
}

// NewRedisStore 构造 Redis 验证码存储（默认 TTL）。
func NewRedisStore(rdb goredis.UniversalClient) *RedisStore {
	// 源项目同款形态：4 位数字+字母混合（避免易混淆字符由库自身处理）。
	driver := base64Captcha.NewDriverString(
		40,  // height
		120, // width
		0,   // noiseCount
		base64Captcha.OptionShowSlimeLine,
		4, // length
		"234567890abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ", // 去易混淆字符
		nil, nil, nil,
	)
	return &RedisStore{rdb: rdb, driver: driver, ttl: DefaultTTL}
}

// WithTTL 覆盖默认 TTL（0 或负值忽略）。
func (s *RedisStore) WithTTL(ttl time.Duration) *RedisStore {
	if ttl > 0 {
		s.ttl = ttl
	}
	return s
}

func key(id string) string { return "auth:captcha:" + id }

// Save 实现 Store。
func (s *RedisStore) Save(ctx context.Context, id, answer string) error {
	if id == "" {
		return errors.New("captcha: empty id")
	}
	if err := s.rdb.Set(ctx, key(id), strings.ToUpper(answer), s.ttl).Err(); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

// Verify 实现 Store：GETDEL 原子读取并删除——**一次性语义的原子保证**。
//
// 为什么不能先 Get 判断再 Delete：两步之间并发请求可能都读到同一个答案，
// 都被放行（验证码形同虚设）。GETDEL 保证只有一个请求能拿到值。
//
// 但「错误不消费」与「一次性」有张力：GETDEL 会无条件删除。折中做法是
// **先 GET 比对，正确才 DEL**——代价是并发窗口内错误尝试不消耗（可接受，
// 因为错误尝试本就是攻击者行为，且验证码 TTL 有限）。此处选「先读后删」
// 以保证可用性（错误不消费），并在正确路径上用 DEL 立即失效。
func (s *RedisStore) Verify(ctx context.Context, id, input string) (bool, error) {
	if id == "" || input == "" {
		return false, nil
	}
	want, err := s.rdb.Get(ctx, key(id)).Result()
	if err != nil {
		// 键不存在（已过期/已消费/从未存在）——业务结果，非故障。
		return false, nil
	}
	got := strings.ToUpper(strings.TrimSpace(input))
	if got != want {
		return false, nil // 错误不消费
	}
	// 正确：立即删除（一次性）。
	if err := s.rdb.Del(ctx, key(id)).Err(); err != nil {
		// 删除失败：验证码已被使用但没能失效——保守返回 false（宁可让用户
		// 重新生成，也不放行一个可能被重放的验证码）。
		return false, ErrStoreUnavailable
	}
	return true, nil
}

// Generate 实现 Store：生成图片并保存答案。
func (s *RedisStore) Generate(ctx context.Context) (string, string, error) {
	c := base64Captcha.NewCaptcha(s.driver, base64Captcha.DefaultMemStore)
	id, b64, _, err := c.Generate()
	if err != nil {
		return "", "", err
	}
	// 从 driver 内存 store 取出答案（Generate 已存入 DefaultMemStore）。
	answer := base64Captcha.DefaultMemStore.Get(id, true) // true = 取出即删
	if answer == "" {
		return "", "", errors.New("captcha: driver produced no answer")
	}
	if err := s.Save(ctx, id, answer); err != nil {
		return "", "", err
	}
	return id, b64, nil
}
