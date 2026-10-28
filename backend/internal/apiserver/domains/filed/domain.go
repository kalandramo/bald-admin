// Package filed 是 file 域的装配声明包（特殊装配）。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：对象存储三件（桥接/minio 直连/兜底桶名）经 Deps getter 迟到取用——
// 装配层 BeforeStart 的 WireStorage/Configure 已完成「minio 与 s3 互斥择一 +
// 桶名注入」的抉择，域只消费产物（不走 App.Resource：资源面只有
// 原始 Storage 实例，域内还得复制适配器分派与桶名解析，反而把装配策略劈两处）。
// Wire 的补注语义：SetObjectStorage 对 nil 不覆盖
// （biz 侧约定），桥接未就绪时保留构造期 minio 直连；两者皆 nil（storage 段
// 未配置）即 biz 判 nil 返回明确错误。
package filed

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	filev1 "github.com/kalandramo/bald-admin/api/gen/go/file/v1"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file/filestore"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	miniooss "github.com/kalandramo/bald/oss/minio"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps）。
type Deps struct {
	// Store 文件元数据仓储（InitBridges 的 repos.File 同一实例，无双构造）。
	Store func() *store.Store[authmodel.File]
	// ObjectStorage 配置驱动的对象存储桥接（WireStorage 产物，minio/s3 适配器
	// 择一；nil=未配置，SetObjectStorage 对 nil 不覆盖）。
	ObjectStorage func() filestore.ObjectStorage
	// Minio minio 直连实例（storage.minio 段产物；New 构造入参，未配置为 nil）。
	Minio func() *miniooss.Storage
	// Bucket 兜底桶名（file.bucket 配置，经 bootstrap.Configure 注入；空=沿用
	// 源默认 "files"）。
	Bucket func() string
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// Domain 返回 file 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain；Services 委托 secretgrpc.NewFileServer——传输适配
// 不搬家）。
func Domain(d Deps) appkit.Domain {
	var biz *filebiz.Biz
	dom := domains.StandardDomain("file", "filed",
		func(context.Context, *appkit.App) error {
			st, err := domains.RequireStore("filed", "file store", d.Store())
			if err != nil {
				return err
			}
			biz = filebiz.New(st, d.Minio(), d.Bucket())
			// 补注：桥接就绪即覆盖构造期 minio
			// 直连（s3 只有桥接形态；minio 桥接与直连等价）；nil 不覆盖。
			biz.SetObjectStorage(d.ObjectStorage(), d.Bucket())
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("filed", h)
			if err != nil {
				return err
			}
			hgin.RegisterFile(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
	dom.Services = func(s *grpc.Server) {
		filev1.RegisterFileServiceServer(s, secretgrpc.NewFileServer(biz))
	}
	return dom
}
