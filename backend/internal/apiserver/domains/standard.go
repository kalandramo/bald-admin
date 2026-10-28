package domains

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kalandramo/bald/pkg/appkit"
)

// StandardDomain 收敛「biz 节点 + routes 节点」这一通用域拓扑：20 个域在
// 迁移期同形（构造 biz、委托 hgin 注册路由），逐域手写 Declare 与执行 switch
// 只是复制粘贴，拓扑本身没有差异。
//
//   - build 在 domain.<name>.biz（wire 相）执行——构造领域能力；
//   - mount 在 domain.<name>.routes（serve 相，依赖 biz 节点）执行——仅当声明了
//     HTTP 面（WithHTTP/WithGatewayRegister 的业务 handler）时投影该节点。
//
// Services 不在本函数内：gRPC 注册是 server 构建时的延迟回调（grpc.Server 在
// 域窗口里不存在），调用方在自己的 Domain.Services 上挂接。
//
// pkg 只作错误文案前缀（与各域既有的「未就绪/断言」错误同款，定位到域包）。
func StandardDomain(name, pkg string, build func(context.Context, *appkit.App) error, mount func(http.Handler) error) appkit.Domain {
	bizKey := appkit.ResourceKey("domain." + name + ".biz")
	routesKey := appkit.ResourceKey("domain." + name + ".routes")
	return appkit.Domain{
		Name: name,
		Declare: func(_ context.Context, s appkit.PlanScope) ([]appkit.Node, error) {
			nodes := []appkit.Node{{Key: bizKey, Phase: appkit.PhaseWire, Priority: 10}}
			if mount == nil || s.Decl.HTTPHandler == nil {
				return nodes, nil
			}
			return append(nodes, appkit.Node{
				Key:       routesKey,
				Segments:  []string{"server.http"},
				Phase:     appkit.PhaseServe,
				Priority:  10,
				DependsOn: []appkit.ResourceKey{bizKey},
			}), nil
		},
		ApplyNode: func(ctx context.Context, app *appkit.App, node appkit.Node) error {
			if node.Key == bizKey {
				return build(ctx, app)
			}
			if node.Key == routesKey && mount != nil {
				h := app.HTTPHandler()
				if h == nil {
					return fmt.Errorf("%s: declared node %q has no HTTP face at execution time", pkg, node.Key)
				}
				return mount(h)
			}
			return fmt.Errorf("%s: unknown declared node %q", pkg, node.Key)
		},
	}
}
