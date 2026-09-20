package filestore

import (
	"context"
	"fmt"
	"io"

	miniov7 "github.com/minio/minio-go/v7"

	miniooss "github.com/kalandramo/bald/oss/minio"
)

// MinioAdapter 把 `bald/oss/minio.Storage` 适配为 ObjectStorage。
//
// minio 的签名本就逐调用传 bucket，故适配是**直通**——无需转换。
type MinioAdapter struct {
	st *miniooss.Storage
}

// NewMinioAdapter 包装 minio Storage（nil 时返回 nil，调用方按 nil 降级）。
func NewMinioAdapter(st *miniooss.Storage) *MinioAdapter {
	if st == nil {
		return nil
	}
	return &MinioAdapter{st: st}
}

// PutObject 上传（minio 原生签名即逐调用传 bucket）。
func (a *MinioAdapter) PutObject(ctx context.Context, bucket, key string, body io.Reader, contentType string) (PutResult, error) {
	info, err := a.st.PutObject(ctx, bucket, key, body, contentType)
	if err != nil {
		return PutResult{}, fmt.Errorf("minio: put object: %w", err)
	}
	return PutResult{Key: info.Key}, nil
}

// GetObject 读取（minio 返回 *minio.Object，已实现 io.ReadCloser）。
func (a *MinioAdapter) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	obj, err := a.st.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("minio: get object: %w", err)
	}
	return obj, nil
}

// RemoveObject 删除对象。
func (a *MinioAdapter) RemoveObject(ctx context.Context, bucket, key string) error {
	if err := a.st.SDK().RemoveObject(ctx, bucket, key, miniov7.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("minio: remove object: %w", err)
	}
	return nil
}

// BucketExists 判断桶是否存在。
func (a *MinioAdapter) BucketExists(ctx context.Context, bucket string) (bool, error) {
	ok, err := a.st.SDK().BucketExists(ctx, bucket)
	if err != nil {
		return false, fmt.Errorf("minio: bucket exists: %w", err)
	}
	return ok, nil
}

// MakeBucket 创建桶。
func (a *MinioAdapter) MakeBucket(ctx context.Context, bucket string) error {
	if err := a.st.SDK().MakeBucket(ctx, bucket, miniov7.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("minio: make bucket: %w", err)
	}
	return nil
}

// 编译期断言。
var _ ObjectStorage = (*MinioAdapter)(nil)
