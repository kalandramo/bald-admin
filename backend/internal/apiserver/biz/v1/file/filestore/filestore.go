// Package filestore 定义文件模块的对象存储抽象（Wave 5.4）。
//
// ## 为何需要这层抽象（签名差异，计划 5.4 的核心验收点）
//
// bald 提供两个对象存储后端，**签名相反**：
//   - `bald/oss/minio`：`PutObject(ctx, bucket, key, body, contentType)`——
//     **逐调用传 bucket**；
//   - `bald/oss/s3`：`PutObject(ctx, key, body, contentType)`——**bucket 在
//     构造期固定**（`Config.Bucket`），调用点不传。
//
// 业务（file biz）需要「按 MIME 分桶」的动态 bucket，两者语义不同。本接口
// 统一为**逐调用传 bucket**（业务语义需要），由各适配器吸收差异：
// s3 适配器在构造期用 `Bucket` 占位、运行期用底层 SDK 覆盖 bucket。
//
// ## 为何不用框架 Storage 门面直接抽象
//
// 两个后端的 SDK 类型不同（`*minio.Client` vs `*awss3.Client`），方法集也不同：
// minio 有 `BucketExists`/`MakeBucket`/`RemoveObject`，s3 封装**只有**
// PutObject/GetObject（缺建桶/删对象/列举/presign）——故 s3 适配器必须落到底层
// `SDK()` 直调。这是框架能力缺口，已记录（见文件尾注）。
package filestore

import (
	"context"
	"io"
)

// PutResult 是一次上传的结果（业务需确认后的对象键落元数据）。
//
// 不含 size：minio 的 UploadInfo 有 Size 而 s3 的 PutObjectOutput 没有——
// 强行统一会迫使 s3 适配器去探测 body 长度（上传后 body 已消费，不可靠）。
// size 由调用方用自己的 content 长度提供（它本就有），接口保持最小。
type PutResult struct {
	// Key 是上传后确认的对象键（供元数据重建）。
	Key string
}

// ObjectStorage 是文件模块依赖的对象存储抽象（逐调用传 bucket）。
type ObjectStorage interface {
	// PutObject 上传对象（bucket/key/body/contentType 逐调用传）。
	PutObject(ctx context.Context, bucket, key string, body io.Reader, contentType string) (PutResult, error)
	// GetObject 读取对象（返回的 ReadCloser 由调用方关闭）。
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	// RemoveObject 删除对象。
	RemoveObject(ctx context.Context, bucket, key string) error
	// BucketExists 判断桶是否存在。
	BucketExists(ctx context.Context, bucket string) (bool, error)
	// MakeBucket 创建桶（幂等语义由调用方保证——先 BucketExists 再 MakeBucket）。
	MakeBucket(ctx context.Context, bucket string) error
}
