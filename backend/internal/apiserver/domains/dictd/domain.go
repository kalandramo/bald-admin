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
	"fmt"

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
	// Authn 认证器（带吊销检查；路由节点执行时已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；路由节点执行时已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 dict 域的装配声明值（ApplyNode 形态：biz 节点构造领域能力，
// routes 节点委托 hgin.RegisterDict；Services 是 server 构建时的延迟注册
// 回调——grpc.Server 在域窗口里还不存在，故服务注册不经 ApplyNode）。
func Domain(d Deps) appkit.Domain {
	var biz *dictbiz.Biz
	return appkit.Domain{
		Name:    "dict",
		Declare: declare,
		ApplyNode: func(_ context.Context, app *appkit.App, node appkit.Node) error {
			switch node.Key {
			case "domain.dict.biz":
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
			case "domain.dict.routes":
				e, err := domains.AssertGin("dictd", app.HTTPHandler())
				if err != nil {
					return err
				}
				hgin.RegisterDict(e, d.Authn(), d.Authz(), biz)
				return nil
			}
			return fmt.Errorf("dictd: unknown declared node %q", node.Key)
		},
		Services: func(s *grpc.Server) {
			dictv1.RegisterDictTypeServiceServer(s, secretgrpc.NewDictTypeServer(biz))
			dictv1.RegisterDictEntryServiceServer(s, secretgrpc.NewDictEntryServer(biz))
		},
	}
}

// declare 声明域的拓扑：biz 节点恒有（构造领域能力），HTTP 面存在才有路由
// 节点。服务注册不在计划里——它由 Services 槽位在 server 构建时兑现，域窗口
// 内没有 grpc.Server 可注册。
func declare(_ context.Context, s appkit.PlanScope) ([]appkit.Node, error) {
	nodes := []appkit.Node{{
		Key:      "domain.dict.biz",
		Phase:    appkit.PhaseWire,
		Priority: 10,
	}}
	if s.Decl.HTTPHandler == nil {
		return nodes, nil
	}
	return append(nodes, appkit.Node{
		Key:       "domain.dict.routes",
		Segments:  []string{"server.http"},
		Phase:     appkit.PhaseServe,
		Priority:  10,
		DependsOn: []appkit.ResourceKey{"domain.dict.biz"},
	}), nil
}
