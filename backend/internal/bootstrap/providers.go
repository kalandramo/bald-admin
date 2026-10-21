// providers.go 是 bootstrap 桥接变量的注入面：契约装配阶段 B 的产物
// （app.Database("sql") / app.Cache("redis") / app.Storage(...)）经 main 的
// WithBeforeStart 钩子（阶段 B 之后）注入桥接包级变量；未注入时 InitBridges
// 回退自建（既有测试/独立调用零改动）。
package bootstrap

import (
	"gorm.io/gorm"

	"github.com/kalandramo/bald/cache"

	miniooss "github.com/kalandramo/bald/oss/minio"
	s3oss "github.com/kalandramo/bald/oss/s3"

	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file/filestore"
)

// WireDatabase 注入契约装配的应用主库（app.Database("sql") 的结果）。
// 断言收敛在桥接包：main 侧不 import gorm。nil 透传为 no-op。
func WireDatabase(v any) {
	if db, ok := v.(*gorm.DB); ok && db != nil {
		DB = db
	}
}

// WireCache 注入契约装配的 KV 缓存实例（app.Cache("redis") 的结果）。
// 同时经 UseRedisClient 把底层 client 记入 RedisClient（审计流复用）。
func WireCache(v any) {
	if c, ok := v.(cache.Cache); ok && c != nil {
		RedisCache = c
	}
}

// WireStorage 注入契约装配的对象存储实例（app.Storage(...) 的结果）。
//
// 支持两个后端——minio（既有）与 s3（新增）。两者的门面类型不同
// （*miniooss.Storage / *s3oss.Storage），故分别记入 MinioStorage 与
// ObjectStorageBridge；main 侧按实际注入的后端调 file biz 的对应 setter。
//
// nil 透传为 no-op（与 WireDatabase/WireCache 同约定）。
func WireStorage(v any) {
	switch st := v.(type) {
	case *miniooss.Storage:
		if st != nil {
			MinioStorage = st
			ObjectStorageBridge = filestore.NewMinioAdapter(st)
		}
	case *s3oss.Storage:
		if st != nil {
			S3Storage = st
			ObjectStorageBridge = filestore.NewS3Adapter(st)
		}
	}
}
