package permgroup

// permgroup_id_test.go —— 策略评估日志 ID 唯一性回归（Windows 时钟粒度缺陷）。
//
// ## 缺陷背景（2026-09-20，Windows 跨设备接力实测发现）
//
// `RecordEvaluation` 的主键由 `fmt.Sprintf("%s:pel:%d", tenantID, now.UnixNano())`
// 生成。Windows 上 `time.Now().UnixNano()` 的**实际分辨率远低于纳秒**——
// 实测连续两次调用 100000 次中有 99999 次返回**同一值**（约 0.5–1ms 粒度）。
//
// 后果：连续两次 RecordEvaluation 落在同一时钟粒度内 → 主键相同 →
// `store: unique constraint conflict` → 审计日志丢失（生产环境为静默丢日志）。
//
// macOS 的时钟粒度更细，故该缺陷在上一台设备（macOS + Docker）被掩盖，
// 在 Windows 上才暴露（e2e `TestWave4_1_EvalLogRead` FAIL）。
//
// ## 不变量
//
// **同一租户下连续两次 RecordEvaluation 必须生成不同的 ID**（无论时钟粒度）。
// 这是 ID 生成器的不变量，与具体时钟实现无关。

import (
	"context"
	"testing"

	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// TestRecordEvaluation_ConsecutiveIDsUnique —— 连续调用必须产生不同 ID。
//
// RED：修复前，连续 N 次调用在同一时钟粒度内撞键，Create 返回 conflict。
// GREEN：修复后，N 次调用全部成功且 ID 互异。
func TestRecordEvaluation_ConsecutiveIDsUnique(t *testing.T) {
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	biz := New()
	ctx := context.Background()

	// 连续写 20 条（远超任何时钟粒度下的撞键概率窗口）。
	// 修复前：Windows 上第 2 条起即 conflict。
	const n = 20
	for i := 0; i < n; i++ {
		err := biz.RecordEvaluation(ctx, "t-default", "u-admin", "secret:get",
			"/v1/secret/1", "GET", true, "")
		if err != nil {
			t.Fatalf("第 %d 次 RecordEvaluation 失败（ID 撞键？）: %v", i+1, err)
		}
	}

	// 断言落库数量与 ID 互异。
	logs, err := biz.ListEvalLogs(ctx, "t-default")
	if err != nil {
		t.Fatalf("ListEvalLogs: %v", err)
	}
	seen := map[string]bool{}
	for i := range logs {
		if seen[logs[i].ID] {
			t.Fatalf("发现重复 ID: %s", logs[i].ID)
		}
		seen[logs[i].ID] = true
	}
	if len(logs) < n {
		t.Fatalf("落库数量 %d < 写入数量 %d（有日志被 conflict 吞掉）", len(logs), n)
	}
	t.Logf("连续 %d 次 RecordEvaluation 全部落库，ID 互异", n)
}
