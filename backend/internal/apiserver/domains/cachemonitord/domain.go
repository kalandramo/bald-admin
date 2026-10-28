// Package cachemonitord 是 cachemonitor 域的装配声明包。
// 包定位、依赖取用约定见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无仓储——唯一依赖是底层 Redis 客户端（cache/redis Client()
// 访问器产物：装配层 cache.redis 段/ env 兼容通道构造适配器时把同一实例记入
// bootstrap.RedisClient，装配层 getter 透传，域不复制择一路径）。nil 合法：
// cache 段未配置即监控端点报不可用（biz 判 nil，禁用态降级），故不做「未就绪」
// 守卫，直通构造。proto（api/gen/go/admin 的
// redis_cache_monitor）仅作 REST 面类型来源，无 gRPC server 实现——Services
// 不声明（合法）。
package cachemonitord

import (
	"context"
	"net/http"

	goredis "github.com/redis/go-redis/v9"

	cmbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/cachemonitor"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
)

// Deps 域的外部依赖（装配层 getter 注入）。
type Deps struct {
	// RDB 底层 go-redis 客户端（bootstrap.RedisClient 同一实例——cache/redis
	// 适配器构造时记录；nil=未配置 cache 段，监控端点降级报不可用）。
	RDB func() goredis.UniversalClient
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 cachemonitor 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain）：routes 委托 hgin.RegisterRedisCacheMonitor——传输
// 适配不搬家；无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *cmbiz.Biz
	return domains.StandardDomain("cachemonitor", "cachemonitord",
		func(context.Context, *appkit.App) error {
			biz = cmbiz.New(d.RDB())
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("cachemonitord", h)
			if err != nil {
				return err
			}
			hgin.RegisterRedisCacheMonitor(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
}
