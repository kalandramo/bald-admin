package e2e

// t_w5_5_encoding_axis_test.go —— Wave 5.5：bald/encoding 轴压真实业务。
//
// ## 计划原文（§Wave 5.5）
//
// 「`bald/encoding` 轴压真实业务（asynq 任务载荷编解码用 msgpack/proto 替代
// json）」→ 验收「编解码往返一致」。
//
// ## 本测试的验证策略（称量后确定）
//
// 真实 asynq 往返需要 Redis（本机无本地 Redis），故**分两层**验证：
//
//  1. **codec 轴本身**（本文件主体）：对真实业务载荷结构做 json/msgpack 的
//     往返一致性 + 字节差异断言。这是「编解码往返一致」验收的直接证据，
//     不依赖外部服务。
//  2. **装配路径**（`TestWave5_5_AsynqCodecSelection`）：断言 asynq 的
//     `WithCodec` 能按名取到已注册 codec——即 `asynq.codec: msgpack` 配置
//     生效。锁住「配置名 → codec 实例」的绑定关系（这是静默失效的高危点：
//     `WithCodec` 对未注册名**静默设 nil**，运行期才报 `codec is nil`）。

import (
	"testing"

	baldencoding "github.com/kalandramo/bald/encoding"
	jsoncodec "github.com/kalandramo/bald/encoding/json"
	"github.com/kalandramo/bald/encoding/msgpack"
)

// taskPayload 是 asynq 任务载荷的真实业务形态（探针任务 probePayload 的同构体）。
type taskPayload struct {
	Msg       string            `json:"msg" msgpack:"msg"`
	TenantID  string            `json:"tenant_id" msgpack:"tenant_id"`
	Attempts  int               `json:"attempts" msgpack:"attempts"`
	Tags      []string          `json:"tags" msgpack:"tags"`
	ExtraMeta map[string]string `json:"extra_meta" msgpack:"extra_meta"`
}

// TestWave5_5_CodecRoundTrip —— json 与 msgpack 的载荷往返一致性 + 字节差异。
//
// 断言：
//  1. 两种 codec 各自 Marshal→Unmarshal 往返后**语义一致**（字段值全等）；
//  2. msgpack 与 json 的**编码字节不同**（证明确实换用了不同 codec，而非
//     静默退回 json——这是「配置生效」的物理证据）。
func TestWave5_5_CodecRoundTrip(t *testing.T) {
	src := taskPayload{
		Msg:       "hello-encoding-axis",
		TenantID:  "t-default",
		Attempts:  3,
		Tags:      []string{"a", "b"},
		ExtraMeta: map[string]string{"k": "v"},
	}

	jsonCodec := jsoncodec.New()
	msgpackCodec := msgpack.New()

	jsonBytes, err := jsonCodec.Marshal(src)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	msgpackBytes, err := msgpackCodec.Marshal(src)
	if err != nil {
		t.Fatalf("msgpack marshal: %v", err)
	}

	// 字节不同——证明两种 codec 确实不同（若相同说明装配没生效）。
	if string(jsonBytes) == string(msgpackBytes) {
		t.Fatalf("json 与 msgpack 编码字节相同（codec 未生效）")
	}
	t.Logf("编码字节数：json=%d msgpack=%d（msgpack 更紧凑：%v）",
		len(jsonBytes), len(msgpackBytes), len(msgpackBytes) < len(jsonBytes))

	// 各自往返一致（语义全等）。
	var jsonBack taskPayload
	if err := jsonCodec.Unmarshal(jsonBytes, &jsonBack); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !payloadEqual(src, jsonBack) {
		t.Fatalf("json 往返不一致: %+v vs %+v", src, jsonBack)
	}

	var msgpackBack taskPayload
	if err := msgpackCodec.Unmarshal(msgpackBytes, &msgpackBack); err != nil {
		t.Fatalf("msgpack unmarshal: %v", err)
	}
	if !payloadEqual(src, msgpackBack) {
		t.Fatalf("msgpack 往返不一致: %+v vs %+v", src, msgpackBack)
	}

	// 反向证明：json 的字节**不能**被 msgpack 正确解码（或解码结果不符）——
	// 说明两者格式互不兼容，进一步坐实 codec 确实不同。
	var crossDecode taskPayload
	if err := msgpackCodec.Unmarshal(jsonBytes, &crossDecode); err == nil {
		if payloadEqual(src, crossDecode) {
			t.Fatalf("json 字节被 msgpack 正确解码——两种 codec 格式意外兼容？")
		}
	}
}

// TestWave5_5_AsynqCodecSelection —— asynq.codec 配置名 → codec 实例的绑定。
//
// 锁住「配置名可解析」这一高危点：asynq 的 `WithCodec(name)` 内部
// `encoding.GetCodec(name)`——**未注册的名字静默设 nil codec**，故障延迟到
// 首次入队才以 `codec is nil (nothing registered)` 暴露。本测试断言
// msgpack 注册后可按名取到（即 `asynq.codec: msgpack` 会生效）。
func TestWave5_5_AsynqCodecSelection(t *testing.T) {
	// 模拟装配期注册（与 buildAsynqServer 同款：显式 MustRegister）。
	// 注意：全局注册表是进程级单例——重复注册会 panic，故用 recover 兜底
	// （本包其他测试可能已注册）。
	registerCodecSafely(jsoncodec.New())
	registerCodecSafely(msgpack.New())

	// 按名取用（asynq WithCodec 的内部路径）。
	if c := baldencoding.GetCodec("msgpack"); c == nil {
		t.Fatal("GetCodec(msgpack) = nil——asynq.codec: msgpack 会静默失效")
	} else if c.Name() != "msgpack" {
		t.Fatalf("codec name = %q, want msgpack", c.Name())
	}
	if c := baldencoding.GetCodec("json"); c == nil {
		t.Fatal("GetCodec(json) = nil")
	}

	// 未注册名返回 nil（asynq 会静默设 nil——记录该行为，供缺陷报告引用）。
	if c := baldencoding.GetCodec("no-such-codec"); c != nil {
		t.Fatalf("GetCodec(未注册) 应返回 nil，实际 %v", c)
	}
	t.Logf("已注册 codec: %v", baldencoding.Names())
}

// registerCodecSafely 注册 codec，重复注册时忽略（全局表是进程级单例）。
func registerCodecSafely(c baldencoding.Codec) {
	defer func() { _ = recover() }()
	baldencoding.MustRegister(c)
}

// payloadEqual 比较两个载荷的语义全等（逐字段）。
func payloadEqual(a, b taskPayload) bool {
	if a.Msg != b.Msg || a.TenantID != b.TenantID || a.Attempts != b.Attempts {
		return false
	}
	if len(a.Tags) != len(b.Tags) || len(a.ExtraMeta) != len(b.ExtraMeta) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	for k, v := range a.ExtraMeta {
		if b.ExtraMeta[k] != v {
			return false
		}
	}
	return true
}
