package e2e

// t_w5_4_s3_storage_e2e_test.go —— Wave 5.4：bald/oss/s3 后端真实验证。
//
// ## 验证目标（计划 §Wave 5.4）
//
// 「`storage` 的 file_transfer / oss 契约段 + `bald/oss/s3` 后端验证（注意
// `PutObject` 签名与 minio 相反）」→ 验收「e2e：S3 上传/下载；签名差异处理正确」。
//
// ## 真实验证（§0 硬性：外部依赖禁止 fake/mock/stub）
//
// 用 `bald/oss/s3` 客户端连**真实 S3 兼容服务**（本地 MinIO 或远程 MinIO，
// 经 env 注入），完成 PutObject → GetObject 往返。缺 env 则整组 Skip。
//
//	BALD_ADMIN_TEST_S3_ENDPOINT   host:port（必填，缺省整组 Skip）
//	BALD_ADMIN_TEST_S3_ACCESS_KEY （必填）
//	BALD_ADMIN_TEST_S3_SECRET_KEY （必填）
//	BALD_ADMIN_TEST_S3_REGION     （默认 us-east-1）
//	BALD_ADMIN_TEST_S3_USE_SSL    "true"/"false"（默认 false）
//	BALD_ADMIN_TEST_S3_BUCKET     （必填，须已存在的桶——s3 封装无建桶 API）
//
// ## 签名差异（本波核心验收点）
//
// `bald/oss/s3` 的 bucket **构造期固定**（`Config.Bucket`），其封装版
// `PutObject(ctx, key, body, contentType)` 不传 bucket；而 `filestore.ObjectStorage`
// 接口要求**逐调用传 bucket**（业务按 MIME 动态分桶）。本测试用
// `filestore.NewS3Adapter` 适配器，断言：
//  1. 适配器用**构造期占位 bucket** 建 Storage，却能用**逐调用的不同 bucket**
//     完成上传/下载——证明签名差异被正确吸收；
//  2. 往返内容一致。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	s3oss "github.com/kalandramo/bald/oss/s3"

	"github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file/filestore"
)

// s3TestConfig 从 env 读 s3 测试配置（缺必填项则 Skip）。
func s3TestConfig(t *testing.T) (endpoint, ak, sk, region, bucket string, useSSL bool) {
	t.Helper()
	endpoint = os.Getenv("BALD_ADMIN_TEST_S3_ENDPOINT")
	ak = os.Getenv("BALD_ADMIN_TEST_S3_ACCESS_KEY")
	sk = os.Getenv("BALD_ADMIN_TEST_S3_SECRET_KEY")
	bucket = os.Getenv("BALD_ADMIN_TEST_S3_BUCKET")
	if endpoint == "" || ak == "" || sk == "" || bucket == "" {
		t.Skip("BALD_ADMIN_TEST_S3_* not set: skip real-S3 e2e (no fake allowed)")
	}
	region = os.Getenv("BALD_ADMIN_TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	useSSL = os.Getenv("BALD_ADMIN_TEST_S3_USE_SSL") == "true"
	return
}

// TestWave5_4_S3AdapterRoundTrip —— s3 适配器上传/下载往返 + 签名差异吸收。
func TestWave5_4_S3AdapterRoundTrip(t *testing.T) {
	endpoint, ak, sk, region, bucket, useSSL := s3TestConfig(t)

	// **关键**：构造期用一个**占位 bucket**（"placeholder"），证明适配器不依赖
	// 它——真实上传走逐调用的 bucket 参数。
	st := s3oss.NewStorage(&s3oss.Config{
		Endpoint: endpoint, Region: region,
		AccessKey: ak, SecretKey: sk,
		UseSsl: useSSL, ForcePathStyle: true,
		Bucket: "placeholder-not-used",
	})
	if st == nil || st.SDK() == nil {
		t.Fatalf("s3 NewStorage failed for endpoint %s", endpoint)
	}
	adapter := filestore.NewS3Adapter(st)
	if adapter == nil {
		t.Fatal("NewS3Adapter returned nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 桶存在性（走底层 SDK——框架封装无 BucketExists）。
	exists, err := adapter.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("BucketExists(%s): %v", bucket, err)
	}
	if !exists {
		t.Fatalf("bucket %s 不存在（测试须用已存在的桶——s3 封装无建桶 API）", bucket)
	}

	key := fmt.Sprintf("w5-4-probe/%d.txt", time.Now().UnixNano())
	content := []byte("hello-bald-s3-adapter-" + time.Now().Format("150405.000000"))

	// 上传：**逐调用传 bucket**（非构造期的 placeholder）。
	res, err := adapter.PutObject(ctx, bucket, key, bytes.NewReader(content), "text/plain")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if res.Key != key {
		t.Fatalf("PutResult.Key=%q, want %q", res.Key, key)
	}

	// 下载：同样逐调用传 bucket。
	rc, err := adapter.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("往返内容不一致: got %q want %q", got, content)
	}
	t.Logf("s3 往返成功: bucket=%s key=%s len=%d（签名差异已吸收：构造期 placeholder，实际用 %s）",
		bucket, key, len(got), bucket)

	// 清理（走底层 SDK——框架封装无 RemoveObject）。
	if err := adapter.RemoveObject(ctx, bucket, key); err != nil {
		t.Fatalf("RemoveObject: %v", err)
	}
	// 删除后读应失败。
	if _, err := adapter.GetObject(ctx, bucket, key); err == nil {
		t.Fatal("删除后 GetObject 应报错")
	}
}
