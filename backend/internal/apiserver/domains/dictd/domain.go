// Package dictd 是 dict 域的装配声明包。包定位、
// 依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 差异两处：① biz 构造带 Cache-Aside 后端（同 secretd，抉择住装配层，经
// Deps.Cache getter 迟到取用）；② gRPC 面是**双 service**（DictType + DictEntry
// 共用同一 dict biz）——Services 一次回调登记两条。
// language 域依附判定：biz/v1/language 与 dict 零 import 关系，
// 独立声明包（domains/languaged），不并入本域。
package dictd

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/cache"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// TypeStore 字典类型仓储（InitBridges 的 repos.DictType 同一实例）。
	TypeStore func() *store.Store[authmodel.DictType]
	// EntryStore 字典项仓储（repos.DictEntry——与类型仓储同批建立）。
	EntryStore func() *store.Store[authmodel.DictEntry]
	// Cache Cache-Aside 后端（装配层已抉择：RedisCache 优先、env 兜底；
	// nil=禁用缓存，biz 内判 nil 直连 store）。
	Cache func() cache.Cache
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 dict 域的装配声明值（Routes 委托 hgin.RegisterDict、Services
// 委托 secretgrpc.NewDictTypeServer/NewDictEntryServer——传输适配不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *dictbiz.Biz
	return appkit.Domain{
		Name:    "dict",
		Declare: declare,
		Wire: func(_ context.Context, _ *appkit.App) error {
			ts, err := domains.RequireStore("dictd", "dict type store", d.TypeStore())
			if err != nil {
				return err
			}
			es, err := domains.RequireStore("dictd", "dict entry store", d.EntryStore())
			if err != nil {
				return err
			}
			biz = dictbiz.New(ts, es, d.Cache())
			return nil
		},
		Routes: func(h http.Handler) error {
			e, err := domains.AssertGin("dictd", h)
			if err != nil {
				return err
			}
			hgin.RegisterDict(e, d.Authn(), d.Authz(), biz)
			return nil
		},
		Services: func(s *grpc.Server) {
			dictv1.RegisterDictTypeServiceServer(s, secretgrpc.NewDictTypeServer(biz))
			dictv1.RegisterDictEntryServiceServer(s, secretgrpc.NewDictEntryServer(biz))
		},
	}
}

// declare 声明域的拓扑（Domain v2 迁移期形态，bald 设计 §4.6）：HTTP/gRPC 面
// 存在才声明对应节点；实际注册仍由 Routes/Services 完成，引擎把它们与 Wire
// 一起塌缩成兼容接线节点 domain.dict.wire。
func declare(_ context.Context, s appkit.PlanScope) ([]appkit.Node, error) {
	var nodes []appkit.Node
	if s.Decl.HTTPHandler != nil {
		nodes = append(nodes, appkit.Node{
			Key:       "domain.dict.routes",
			Segments:  []string{"server.http"},
			Phase:     appkit.PhaseServe,
			Priority:  10,
			DependsOn: []appkit.ResourceKey{"domain.dict.wire"},
		})
	}
	if s.Config.GetServer().GetGrpc() != nil {
		nodes = append(nodes, appkit.Node{
			Key:       "domain.dict.services",
			Segments:  []string{"server.grpc"},
			Phase:     appkit.PhaseServe,
			Priority:  5,
			DependsOn: []appkit.ResourceKey{"domain.dict.wire"},
		})
	}
	return nodes, nil
}
