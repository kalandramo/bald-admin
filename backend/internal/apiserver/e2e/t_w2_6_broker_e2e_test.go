package e2e

// t_w2_6_broker_e2e_test.go —— `bald/broker` 能力勘探。
//
// ## 勘探性质（重要）
//
// 前提「broker(redis 后端) 承载消息事件总线（**替换 goroutine fanout**）」
// 取证后**被推翻**：
//   1. 源项目 go-wind-admin **根本不用 broker**（`broker` 仅作为
//      `kratos-transport/broker` 的 **indirect** 依赖出现在 go.mod）；
//   2. `bald/broker` 在 appkit **零引用**，真实消费者是
//      `bald/transport/{redis,rabbitmq,kafka}`；
//   3. 契约里的 `broker_address`/`broker_type` 属于 **Machinery** 段，
//      与 `bald/broker` 不是一回事；
//   4. 「跨实例持久化扇出」的收益**已被 asynq 覆盖**（源即如此）。
//
// 故此处只做**能力勘探**——
// 而 broker 是框架里**零消费者的休眠组件**，勘探它就是有价值的产出。
// 不造无意义消费者；为 SSE 跨实例扇出预留通道。
//
// ## 勘探到的两个未文档化约束（探针实测）
//
// 1. **`NewBroker` 不调用 `Init`**——`addr` 只在 `Init()` 里赋值。
//    只 `NewBroker(...)` + `Connect()` → `b.addr` 为空 → `DialURL("")`
//    → `invalid redis URL scheme:`（一个极具误导性的错误信息）。
// 2. ~~**`Connect()` 在 Redis 不可用时返回 nil（假成功）**~~ —— **已于
//    broker/redis v0.1.1 修复**（Connect 取连接 PING 探活，失败即报错）。
//    注意：`Dial` 是懒执行，故障延迟到首次 `Publish`/`Subscribe`
//    才暴露；调用方若在启动期检查 `Connect()` 返回值会误判「已连通」。
//    v0.1.2 又补了探活失败路径的 Disconnect nil 防护；`TestWave2_6_BrokerDegrade`
//    守护该行为。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kalandramo/bald/broker"
	redisbroker "github.com/kalandramo/bald/broker/redis"
	"github.com/kalandramo/bald/broker/redis/option"
)

// brokerTestAddr 返回 broker 测试用的 Redis URL（读 env，回落本地 6379）。
// 函数而非常量：支持经 BALD_E2E_REDIS_ADDR 指向非本地实例。
func brokerTestAddr() string { return redisTestURL() }

// requireRedis 探测 broker 测试依赖的 Redis（地址经 BALD_E2E_REDIS_ADDR
// 可覆盖，默认本地 127.0.0.1:6379）。不可达则 Skip。
//
// `Connect()`（broker/redis v0.1.2 起）已做真实探活（取连接 PING），
// 且实测能区分三种情况（探针验证）：
//   - 不可达      → dial tcp ... connection refused
//   - 认证失败    → WRONGPASS invalid username-password pair
//   - 正常        → nil
//
// 故改用 `Connect()` 判可达性——判据单一（与测试主体同一条代码路径），
// 且顺带覆盖 `Init` 的 addr 赋值，比旁路 Ping 更贴近真实用法。
//
// 选 v0.1.2 而非 v0.1.1 的原因：v0.1.1 的探活失败路径会丢弃 pool
// （`b.pool = nil`）但 Disconnect 无 nil 防护 → 调用即 panic。本函数
// 探测后要 Disconnect，故必须有该防护（v0.1.2 修复）。
func requireRedis(t *testing.T) {
	t.Helper()
	b := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr()))
	if err := b.Init(); err != nil {
		t.Skipf("redis %s 初始化失败，跳过（环境缺失，非验证失败）: %v", redisTestAddr(), err)
	}
	// Connect 失败后 pool 可能已被丢弃——v0.1.2 起 Disconnect 对 nil pool 安全。
	defer func() { _ = b.Disconnect() }()
	if err := b.Connect(); err != nil {
		t.Skipf("redis %s 不可达，跳过（环境缺失，非验证失败）: %v", redisTestAddr(), err)
	}
}

// TestWave2_6_BrokerCrossInstance —— broker pubsub 跨实例扇出（核心能力）。
//
// 发布方与订阅方是**两个独立的 broker 实例**，经 Redis 中转——
// 这正是 broker 相对进程内 channel 的唯一价值。
func TestWave2_6_BrokerCrossInstance(t *testing.T) {
	requireRedis(t) // 独立探测（不能靠 Connect 的假成功判可达性，见 helper 注释）
	topic := "bald-admin.probe." + time.Now().Format("150405.000000")

	// 订阅方（实例 A）。**必须先 Init**。
	sub := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr()))
	if err := sub.Init(); err != nil {
		t.Fatalf("sub.Init: %v", err)
	}
	if err := sub.Connect(); err != nil {
		t.Fatalf("sub.Connect: %v", err) // 可达性已由 requireRedis 保证，此处失败即真故障
	}
	defer sub.Disconnect()

	got := make(chan string, 1)
	if _, err := sub.Subscribe(topic,
		func(_ context.Context, event broker.Event) error {
			// binder 返回的是 *[]byte（指针）——实测确认。
			switch v := event.Message().Body.(type) {
			case *[]byte:
				got <- string(*v)
			case []byte:
				got <- string(v)
			default:
				got <- "unexpected-type"
			}
			return nil
		},
		func() any { var b []byte; return &b },
	); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	time.Sleep(700 * time.Millisecond) // 等订阅注册到 Redis

	// 发布方（实例 B）——独立连接。
	pub := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr()))
	if err := pub.Init(); err != nil {
		t.Fatalf("pub.Init: %v", err)
	}
	if err := pub.Connect(); err != nil {
		t.Fatalf("pub.Connect: %v", err)
	}
	defer pub.Disconnect()

	if err := pub.Publish(context.Background(), topic, &broker.Message{
		Body: []byte(`{"msg":"hello-cross-instance"}`),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case v := <-got:
		if !strings.Contains(v, "hello-cross-instance") {
			t.Fatalf("收到内容不符: %s", v)
		}
		t.Logf("跨实例扇出成功: %s", v)
	case <-time.After(3 * time.Second):
		t.Fatal("订阅方未收到——跨实例扇出失败")
	}
}

// TestWave2_6_BrokerRequiresInit —— 回归：不调 Init 则连接失败。
//
// 锁住该约束，防止后人误以为 NewBroker 已完成初始化。
//
// Connect 做真实探活后，空 addr 在 **Connect 阶段**即失败（fail-fast）——
// 断言随之锚定在 Connect（此前错误延迟到 Subscribe 才报
// `invalid redis URL scheme`）。
//
// **因此本测试不再需要 Redis 环境**：空 addr 在任何网络操作前就失败，
// 与 Redis 可达性无关。原先的 requireRedis 反而会掩盖断言（无 Redis 时
// 整个测试被 SKIP，从未真正执行）。
func TestWave2_6_BrokerRequiresInit(t *testing.T) {
	// 刻意**不调 Init**——addr 为空。
	b := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr()))

	// Connect 立即失败（fail-fast），不再假成功。
	err := b.Connect()
	if err == nil {
		t.Fatal("不调 Init 时 Connect 应失败（D13.1 约束被破坏？addr 为空应报错）")
	}
	t.Logf("确认 D13.1（Connect 阶段暴露）: %v", err)
	// 错误信息是误导性的 `invalid redis URL scheme:`——addr 为空所致。
	// 不硬断言文案（框架可能改进措辞），仅记录。
	if strings.Contains(err.Error(), "URL scheme") {
		t.Log("错误文案仍为 'invalid redis URL scheme:'（addr 为空所致，具误导性）")
	}
}

// TestWave2_6_BrokerDegrade —— 降级路径。
//
// `Connect` 对不可达 Redis 必须报错（broker/redis v0.1.1 起 Connect 取连接
// PING 探活）——否则故障延迟到首次 Publish/Subscribe，调用方信任返回值会
// 在启动期误判「已连通」。本测试守护该行为。
//
// 本测试**不依赖 Redis 环境**（指向无监听的 6399 端口），故不受 env 影响。
func TestWave2_6_BrokerDegrade(t *testing.T) {
	// 指向无服务监听的端口。
	b := redisbroker.NewBroker(option.DriverTypePubSub,
		broker.WithAddress("redis://127.0.0.1:6399"))
	if err := b.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Connect 对不可达 Redis 必须报错（不得假成功返回 nil）。
	if err := b.Connect(); err == nil {
		t.Fatal("Connect 对不可达 Redis 应返回 error（D13.2 已在 v0.1.1 修复，nil 即回归）")
	} else {
		t.Logf("确认 D13.2 已修：Connect() 报错 = %v", err)
	}

	// 真实故障在首次操作时暴露（Dial 懒执行路径）。
	_, err := b.Subscribe("t.degrade",
		func(context.Context, broker.Event) error { return nil },
		func() any { var x []byte; return &x })
	if err == nil {
		t.Fatal("Subscribe 对不可达 Redis 应报错")
	}
	t.Logf("Subscribe 暴露真实故障: %v", err)

	if err := b.Publish(context.Background(), "t.degrade",
		&broker.Message{Body: []byte("x")}); err == nil {
		t.Fatal("Publish 对不可达 Redis 应报错")
	}

	// Disconnect 不应挂起，且**不得 panic**。
	// 关键：Connect 探活失败会丢弃 pool（b.pool = nil），若 Disconnect 无
	// nil 防护则 panic——v0.1.1 有此缺陷，v0.1.2 修复。本断言即其端到端守护。
	done := make(chan struct{})
	go func() { _ = b.Disconnect(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Disconnect 挂起")
	}
}

// TestWave2_6_BrokerStreamDriver —— 另一 driver（stream）是否同样需 Init。
//
// 两 driver 的 NewBroker 都不调 Init——同族约束（覆盖两者）。
func TestWave2_6_BrokerStreamDriver(t *testing.T) {
	requireRedis(t)
	b := redisbroker.NewBroker(option.DriverTypeStream, broker.WithAddress(brokerTestAddr()))
	// 同样必须显式 Init。
	if err := b.Init(); err != nil {
		t.Fatalf("stream Init: %v", err)
	}
	if err := b.Connect(); err != nil {
		t.Fatalf("stream Connect: %v", err)
	}
	defer b.Disconnect()
	t.Logf("stream driver 可用，Name=%s Address=%s", b.Name(), b.Address())
}
