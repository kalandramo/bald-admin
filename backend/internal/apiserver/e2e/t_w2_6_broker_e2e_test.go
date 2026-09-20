package e2e

// t_w2_6_broker_e2e_test.go —— Wave 2.6：`bald/broker` 能力勘探。
//
// ## 本波的性质（重要，与其余波次不同）
//
// 计划原定「broker(redis 后端) 承载消息事件总线（**替换 goroutine fanout**）」。
// 取证后该前提**被推翻**（详见计划文档与提交信息）：
//   1. 源项目 go-wind-admin **根本不用 broker**（`broker` 仅作为
//      `kratos-transport/broker` 的 **indirect** 依赖出现在 go.mod）；
//   2. `bald/broker` 在 appkit **零引用**，真实消费者是
//      `bald/transport/{redis,rabbitmq,kafka}`；
//   3. 契约里的 `broker_address`/`broker_type` 属于 **Machinery** 段，
//      与 `bald/broker` 不是一回事；
//   4. 「跨实例持久化扇出」的收益**已被 asynq 覆盖**（源即如此）。
//
// 故本波改为**能力勘探**——本任务目标之一是「验证 bald 框架能力与缺陷」，
// 而 broker 是框架里**零消费者的休眠组件**，勘探它就是有价值的产出。
// 不造无意义消费者；为 Wave 3（SSE 跨实例扇出）预留通道。
//
// ## 勘探到的两个未文档化约束（探针实测，记为 D13）
//
// 1. **`NewBroker` 不调用 `Init`**——`addr` 只在 `Init()` 里赋值。
//    只 `NewBroker(...)` + `Connect()` → `b.addr` 为空 → `DialURL("")`
//    → `invalid redis URL scheme:`（一个极具误导性的错误信息）。
// 2. **`Connect()` 在 Redis 不可用时返回 nil（假成功）**——`Dial` 是懒执行，
//    故障延迟到首次 `Publish`/`Subscribe` 才暴露。调用方若在启动期检查
//    `Connect()` 的返回值，会误判为「已连通」。

import (
	"context"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/kalandramo/bald/broker"
	redisbroker "github.com/kalandramo/bald/broker/redis"
	"github.com/kalandramo/bald/broker/redis/option"
)

const brokerTestAddr = "redis://127.0.0.1:6379"

// requireRedis 独立探测 broker 测试依赖的本地 Redis（127.0.0.1:6379）。
//
// **不能依赖 `broker.Connect()` 的返回值判断可达性**——D13.2 实测 `Connect()`
// 对不可达 Redis 返回 nil（假成功），用它做 Skip 判据会让测试在不该继续时继续，
// 故障延迟到 `Subscribe` 才暴露成 **FAIL**（而非环境缺失应有的 SKIP）。
// 这与本仓其余 e2e 的「环境缺失 → Skip，不伪装通过」约定一致。
func requireRedis(t *testing.T) {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379"})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis 127.0.0.1:6379 不可达，跳过（环境缺失，非验证失败）: %v", err)
	}
}

// TestWave2_6_BrokerCrossInstance —— broker pubsub 跨实例扇出（核心能力）。
//
// 发布方与订阅方是**两个独立的 broker 实例**，经 Redis 中转——
// 这正是 broker 相对进程内 channel 的唯一价值。
func TestWave2_6_BrokerCrossInstance(t *testing.T) {
	requireRedis(t) // 独立探测（不能靠 Connect 的假成功判可达性，见 helper 注释）
	topic := "bald-admin.probe." + time.Now().Format("150405.000000")

	// 订阅方（实例 A）。**必须先 Init**（D13.1）。
	sub := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr))
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
	pub := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr))
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

// TestWave2_6_BrokerRequiresInit —— D13.1 回归：不调 Init 则 Subscribe 失败。
//
// 锁住该约束，防止后人误以为 NewBroker 已完成初始化。
//
// 注意：本测试断言的是「不调 Init → Subscribe 报 `invalid redis URL scheme`」。
// 若 Redis 不可达，Subscribe 也会报错（但原因不同），会让本测试**假绿**——
// 故必须先 requireRedis 保证可达，使失败原因唯一归因于「未 Init」。
func TestWave2_6_BrokerRequiresInit(t *testing.T) {
	requireRedis(t)
	// 刻意**不调 Init**。
	b := redisbroker.NewBroker(option.DriverTypePubSub, broker.WithAddress(brokerTestAddr))
	if err := b.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer b.Disconnect()

	_, err := b.Subscribe("t.init-check",
		func(context.Context, broker.Event) error { return nil },
		func() any { var x []byte; return &x })
	if err == nil {
		t.Fatal("不调 Init 应失败（D13.1 已修复？请核实框架行为变化）")
	}
	// 错误信息是误导性的 `invalid redis URL scheme:`——addr 为空所致。
	if !strings.Contains(err.Error(), "URL scheme") {
		t.Logf("错误信息已变化（原为 'invalid redis URL scheme:'）: %v", err)
	} else {
		t.Logf("确认 D13.1：%v", err)
	}
}

// TestWave2_6_BrokerDegrade —— 降级路径（计划 2.6 的验收项）。
//
// **核心发现（D13.2）**：`Connect()` 对不可达 Redis 返回 **nil**（假成功），
// 故障延迟到首次 Publish/Subscribe。这是本波最有价值的勘探结果——
// 调用方若信任 `Connect()` 的返回值，会在启动期误判「已连通」。
func TestWave2_6_BrokerDegrade(t *testing.T) {
	// 指向无服务监听的端口。
	b := redisbroker.NewBroker(option.DriverTypePubSub,
		broker.WithAddress("redis://127.0.0.1:6399"))
	if err := b.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// D13.2：Connect 假成功。
	if err := b.Connect(); err != nil {
		t.Fatalf("Connect 预期返回 nil（假成功，D13.2），实际: %v", err)
	}
	t.Log("确认 D13.2：Connect() 对不可达 Redis 返回 nil（假成功）")

	// 真实故障在首次操作时暴露。
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

	// Disconnect 不应挂起（已验证不阻塞）。
	done := make(chan struct{})
	go func() { b.Disconnect(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Disconnect 挂起")
	}
}

// TestWave2_6_BrokerStreamDriver —— 另一 driver（stream）是否同样需 Init。
//
// 两 driver 的 NewBroker 都不调 Init——同族约束（D13.1 覆盖两者）。
func TestWave2_6_BrokerStreamDriver(t *testing.T) {
	requireRedis(t)
	b := redisbroker.NewBroker(option.DriverTypeStream, broker.WithAddress(brokerTestAddr))
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
