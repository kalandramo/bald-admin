package app

// latebinding.go 收敛「构造期创建、请求期调用」的认证/授权绑定（Wave 4.2）。
//
// ## 为什么还需要这层间接（诚实结论）
//
// Wave 4.2 的目标是删除 bootstrap 的 lazy* 适配器族——它们的正当性随 Wave 4.1
// （装配移到 InitBridges 之后）而消失：gin 路由、管理面、Signer、吊销检查器
// 现在都直接持有真实实例。
//
// 但**有两处消费方仍在构造期创建**，早于 InitBridges：
//   - gRPC 拦截器链（servers.go 的 newGRPCServerOptions，经 appkit.WithGRPC
//     在 FromBootstrap 构造期绑定）；
//   - SSE 服务器（buildSSEServer，经 WithExtraServers 在构造期追加）。
//
// 二者的 认证/授权 在**请求期**才被调用，但拦截器/服务器实例在构造期就已持有
// 其接口值——此刻桥接变量尚为 nil。故必须请求期解析。
//
// ## 为什么放在 app 层而非 bootstrap
//
// 该约束的成因是**装配根自己的构造顺序**（gRPC/SSE 在 app 包构造期创建），
// 故间接层归属 app 层更贴切；bootstrap 不再承担「时序变通」的职责。
// 命名沿用本包既有的「迟到绑定」惯用法（appRefT，M10.2 管理面同款）。
//
// ## 与已删除 lazy* 的差别
//
// 已删除的 4 个（lazyAuthn/lazyAuthz/lazySigner/lazyAuthnWithRevocation）是
// **可以也应该消除**的时序变通（gin 侧消费方现已持真实实例）。此处两个是
// **构造顺序的固有约束**——彻底消除需要框架层支持「运行期构造 gRPC 选项」，
// 属后续事项（见 Wave 4.2 交付报告的「后续建议」）。

import (
	"context"

	"github.com/kalandramo/bald/pkg/authn"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// lateAuthn 把认证器推迟到请求期解析（读 InitBridges 建立的桥接变量）。
// 供构造期创建的 gRPC 拦截器链 / SSE 服务器使用。
type lateAuthn struct{}

func (lateAuthn) Authenticate(ctx context.Context) (*authn.AuthClaims, error) {
	return bootstrappkg.Authenticator.Authenticate(ctx)
}

func (lateAuthn) AuthenticateToken(token string) (*authn.AuthClaims, error) {
	return bootstrappkg.Authenticator.AuthenticateToken(token)
}

// lateAuthz 把授权器推迟到请求期解析（读 InitBridges 建立的桥接变量）。
type lateAuthz struct{}

func (lateAuthz) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	return bootstrappkg.Authorizer.Authorize(ctx, subject, object, action)
}
