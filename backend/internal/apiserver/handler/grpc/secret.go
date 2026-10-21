// Package grpc 提供 bald-admin 的 gRPC service 装配（起改用 proto 生成代码）。
//
// 取代 M2 的手写 ServiceDesc + JSON codec 范式：SecretService 由
// api/protos/secret/v1/secret.proto 经 buf generate 生成
// （adminv1.SecretServiceServer / RegisterSecretServiceServer /
// RegisterSecretServiceHandler），并复用 grpc-gateway 把同一 service 转码为 REST。
// 生成代码见 api/gen/go/ 目录（proto 源收 protos/，生成物按语言分层）。
// 原 ListUsers 演示 RPC已由 UserService.ListUsers（user.go）接管 GET /v1/user。
package grpc

import (
	"context"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"

	adminv1 "github.com/kalandramo/bald-admin/api/gen/go/secret/v1"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
)

// secretService 实现生成的 adminv1.SecretServiceServer。
// 必须嵌入 UnimplementedSecretServiceServer：protoc-gen-go-grpc 默认开启
// require_unimplemented_servers，嵌入它才能满足接口（否则编译报
// missing method mustEmbedUnimplementedSecretServiceServer）。
type secretService struct {
	adminv1.UnimplementedSecretServiceServer
	biz *secretbiz.SecretBiz // 删除经 biz 落 store（禁止占位式桥接）
}

// NewServer 构造 SecretServiceServer 实现（biz 由 wire 装配注入）。
func NewServer(biz *secretbiz.SecretBiz) adminv1.SecretServiceServer {
	return &secretService{biz: biz}
}

func (s *secretService) GetSecret(ctx context.Context, req *adminv1.GetSecretRequest) (*adminv1.GetSecretResponse, error) {
	claims := authn.AuthClaimsFromContext(ctx)
	viewer := ""
	if claims != nil {
		viewer = claims.Name
	}
	// 与 gin 侧 GET /v1/secret/:id 同一 biz 路径（cache-aside + 租户隔离），
	// 错误同源：NotFound 哨兵经 grpc 拦截器/gateway 转码后与 gin 面同形。
	// 不经 biz 直连 store 会绕过缓存，且错误与 gin 面 reason 分裂。
	item, err := s.biz.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &adminv1.GetSecretResponse{Id: item.ID, Content: item.Content, Viewer: viewer}, nil
}

func (s *secretService) DeleteSecret(ctx context.Context, req *adminv1.DeleteSecretRequest) (*adminv1.DeleteSecretResponse, error) {
	// 真实删除（与 gin 侧 DELETE /v1/secret/:id 同一 biz 语义）：存在性确认 +
	// store 删除 + Cache-Aside 失效，租户隔离由 Where.T 自动完成。
	// gateway 转码的 DELETE /v1/secrets/{id} 不得假成功——
	// 数据未删即返回错误，不返回 200。
	ok, err := s.biz.Delete(ctx, req.GetId())
	if err != nil || !ok {
		return nil, berrors.NotFound("secret/not_found").WithMessage("secret not found")
	}
	return &adminv1.DeleteSecretResponse{Deleted: req.GetId()}, nil
}
