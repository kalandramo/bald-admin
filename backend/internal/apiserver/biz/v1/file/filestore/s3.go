package filestore

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	s3oss "github.com/kalandramo/bald/oss/s3"
)

// S3Adapter 把 `bald/oss/s3.Storage` 适配为 ObjectStorage。
//
// ## 签名差异的处理（Wave 5.4 核心）
//
// `bald/oss/s3` 的 bucket **在构造期固定**（`Config.Bucket`），其
// `PutObject(ctx, key, body, contentType)` **不接受 bucket 参数**。而业务
// 需要按 MIME 动态分桶（images/files/docs）。
//
// 处理方式：**落到底层 SDK 直调并显式传 bucket**（`SDK()` 返回
// `*awss3.Client`，其 `PutObjectInput.Bucket` 逐调用可传）。这绕过了框架封装
// 的固定 bucket 约束，是本适配器存在的核心理由。
//
// ## 框架能力缺口（已记录，见 filestore.go 头注）
//
// `bald/oss/s3` 封装**只有** PutObject/GetObject——**缺** minio 有的
// `BucketExists`/`MakeBucket`/`RemoveObject`，也缺 presign/list。故本适配器的
// 建桶/删对象/存在性判断**全部落到底层 SDK**，无法复用框架封装。
type S3Adapter struct {
	st     *s3oss.Storage
	client *awss3.Client
}

// NewS3Adapter 包装 s3 Storage（nil 或 SDK nil 时返回 nil）。
func NewS3Adapter(st *s3oss.Storage) *S3Adapter {
	if st == nil || st.SDK() == nil {
		return nil
	}
	return &S3Adapter{st: st, client: st.SDK()}
}

// PutObject 上传（显式传 bucket——覆盖构造期的固定 bucket）。
func (a *S3Adapter) PutObject(ctx context.Context, bucket, key string, body io.Reader, contentType string) (PutResult, error) {
	in := &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := a.client.PutObject(ctx, in); err != nil {
		return PutResult{}, fmt.Errorf("s3: put object: %w", err)
	}
	// s3 的 PutObjectOutput 不回传 key/size（与 minio UploadInfo 不同）——用入参
	// key 回填；size 由调用方用自己的 content 长度提供（见 PutResult 注释）。
	return PutResult{Key: key}, nil
}

// GetObject 读取（返回 Body，调用方负责 Close）。
func (a *S3Adapter) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := a.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3: get object: %w", err)
	}
	return out.Body, nil
}

// RemoveObject 删除对象（框架封装无此方法，落到底层 SDK）。
func (a *S3Adapter) RemoveObject(ctx context.Context, bucket, key string) error {
	if _, err := a.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("s3: remove object: %w", err)
	}
	return nil
}

// BucketExists 判断桶是否存在（框架封装无此方法，落到底层 SDK）。
//
// 用 HeadBucket（比 ListBuckets 更省，且不需要列举权限）。
func (a *S3Adapter) BucketExists(ctx context.Context, bucket string) (bool, error) {
	if _, err := a.client.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		// HeadBucket 对不存在返回 404——按「不存在」处理而非错误（与 minio
		// BucketExists 的 (false, nil) 语义对齐）。
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("s3: head bucket: %w", err)
	}
	return true, nil
}

// MakeBucket 创建桶（框架封装无此方法，落到底层 SDK）。
func (a *S3Adapter) MakeBucket(ctx context.Context, bucket string) error {
	if _, err := a.client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		return fmt.Errorf("s3: make bucket: %w", err)
	}
	return nil
}

// isNotFound 判断错误是否为「不存在」（404 / NoSuchBucket）。
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// AWS SDK 的 API 错误实现了 ErrorCode()；直接断言（不 import smithy 错误包，
	// 保持适配器依赖面最小）。
	if e, ok := err.(interface{ ErrorCode() string }); ok {
		switch e.ErrorCode() {
		case "NotFound", "NoSuchBucket", "NoSuchKey":
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "StatusCode: 404") ||
		strings.Contains(msg, "NoSuchBucket") ||
		strings.Contains(msg, "NotFound")
}

// 编译期断言。
var _ ObjectStorage = (*S3Adapter)(nil)
