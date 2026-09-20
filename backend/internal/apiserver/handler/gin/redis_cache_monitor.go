package gin

// redis_cache_monitor.go Redis 缓存监控 REST handler（Wave 6.2）。只读运维接口，
// admin 专属（object 归一化 "redis_cache_monitor"）。

import (
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/admin/v1"
	cmbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/cachemonitor"
)

// RegisterRedisCacheMonitor 挂载缓存监控路由（需认证 + admin 权限）。
//   - GET /v1/redis-cache-monitor  监控信息（INFO/DBSIZE/SLOWLOG）
func RegisterRedisCacheMonitor(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *cmbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/redis-cache-monitor", authzMW, func(c *gingonic.Context) {
		res, err := biz.Info(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, toMonitorPB(res))
	})
}

// toMonitorPB biz 结果 → proto（fail-soft：空视图也返回 200）。
func toMonitorPB(r *cmbiz.Result) *adminv1.RedisCacheMonitorInfo {
	out := &adminv1.RedisCacheMonitorInfo{DbSize: r.DBSize}
	for _, s := range r.Sections {
		sec := &adminv1.InfoSection{Name: s.Name}
		for _, e := range s.Entries {
			sec.Entries = append(sec.Entries, &adminv1.InfoEntry{Key: e.Key, Value: e.Value})
		}
		out.Sections = append(out.Sections, sec)
	}
	for _, sl := range r.Slowlog {
		out.Slowlog = append(out.Slowlog, &adminv1.SlowLogEntry{
			Id:           sl.ID,
			CreatedAt:    timestamppb.New(sl.CreatedAt),
			DurationUsec: sl.DurationUsec,
			Args:         sl.Args,
			ClientAddr:   sl.ClientAddr,
			ClientName:   sl.ClientName,
		})
	}
	return out
}
