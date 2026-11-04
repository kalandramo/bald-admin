package app

// testaddr_test.go —— 装配类测试的端口分配助手。
//
// ## 为什么需要它（根因记录）
//
// 此前 `assembly_e2e_test.go` / `assembly_middleware_test.go` 使用**硬编码固定端口**
// （:18099 / :18199）。这导致两类问题（本会话实测复现）：
//
//  1. **就绪判据可被冒名顶替**：测试以 `net.Dial(固定端口)` 判定「装配链已跑完」，
//     但这只证明「**某个进程**在监听该端口」，不证明「**我的 server** 在监听」。
//     若有残留进程（前次运行未退净的 server、或其它工具）占着该端口，Dial 立即成功
//     → 测试误判就绪 → 继续断言装配不变量 → 观察到全 nil（如 4 处
//     `Expected value not to be nil`）。**这是假绿/假红**，不只是 flaky。
//  2. **绑定冲突与 TIME_WAIT 累积**：反复运行会留下 TIME_WAIT，后续运行偶发
//     连不上或绑定失败（表现为 `appkit: wait for endpoints canceled: context canceled`）。
//     这句文案后来被查明有两面（bald 风险评审 §4.4）：server 绑定失败经 errgroup
//     取消 `gctx` 后被它整条掩盖，测试自己的 `cancel()` 落在启动窗口内也是它。
//     框架侧的分级已发 `pkg v0.2.2`（一条 `startupOutcome` 判据挂到五处启动分支），
//     本仓库现在消费的就是它。即便如此就绪判据仍不依赖取消时序——见下面
//     `withBootSignal`：真判据是框架的 `AfterStart` 运行态信号，端口可连只是它的
//     事后确认。
//
// 复现方式（已验证）：外部进程占住 :18099 后运行测试 → 稳定失败（非偶发）。
//
// ## 修法：每次运行取一个空闲端口
//
// `freeTCPAddr` 向内核申请 `:0`（由内核返回当前空闲端口），随即释放并在配置中使用
// 该端口号。相比硬编码端口：
//   - 每次运行端口不同 → **不会与残留进程冲突**，也不会累积 TIME_WAIT 到同一端口；
//   - 端口确实空闲（刚由内核分配并释放）；
//   - 仍保留「固定地址轮询就绪」的既有测试结构（改动面最小）。
//
// 残余的 TOCTOU 窗口（释放到 server 绑定之间的极短间隙）由测试串行执行（同一包内
// 测试默认串行，除非显式 t.Parallel）与实际不可复现性覆盖；相比原先「固定端口被
// 任意进程长期占用」的问题，风险已下降数个量级。

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/stretchr/testify/require"
)

// withBootSignal 给 opts 追加一个 AfterStart 钩子，返回「装配链已跑到运行态」的
// 通道。框架在**每个 server 端点定型且注册完成之后**才跑这个钩子，所以它是确定性
// 的就绪判据；用轮询端口能不能连来猜「装配有没有跑完」是它的近似物，近似物会把
// 「还没监听」误读成「还没就绪」——那正是 bald 风险登记册 §4.4 撞过的形状。
//
// 钩子只加在测试里，不进 appOptions（生产装配声明不含测试信令）。
func withBootSignal(opts []appkit.Option) ([]appkit.Option, <-chan struct{}) {
	booted := make(chan struct{})
	return append(opts, appkit.AfterStart(func(context.Context) error {
		close(booted)
		return nil
	})), booted
}

// awaitBoot 等运行态信号。装配链若在信号之前就退出，直接把 Serve 的错误报出来
// ——30s 超时那种「什么都没发生」的失败信息对接手者没有用。
func awaitBoot(t *testing.T, booted <-chan struct{}, runErr <-chan error) {
	t.Helper()
	select {
	case <-booted:
	case err := <-runErr:
		t.Fatalf("装配链在进入运行态前就退出了，err=%v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("60s 内没有收到 AfterStart 运行态信号")
	}
}

// requireDialable 断言每个已声明的 server 端口都能连上。运行态信号之后监听早已
// 建立，这里只留 2s 容差——真连不上就是缺陷，不该靠放宽超时消红。
func requireDialable(t *testing.T, addrs ...string) {
	t.Helper()
	require.NotEmpty(t, addrs, "至少要有一个待判定的 server 端口")
	for _, addr := range addrs {
		require.Eventually(t, func() bool { return canDialTCP(addr) },
			2*time.Second, 100*time.Millisecond,
			"运行态信号之后端口 %s 仍不可连（监听未建立）", addr)
	}
}

// canDialTCP 报告 addr 上是否已有进程在监听——装配类测试的就绪判据用它，
// 且必须对**每个已声明的 server 端口**各问一次（少一个就弱于「装配完成」，
// 理由见 assembly_e2e_test.go 文件头）。
func canDialTCP(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// freeTCPAddr 向内核申请一个空闲 TCP 端口并立即释放，返回形如 "127.0.0.1:54321" 的地址。
//
// 注意：返回的是**刚被释放**的端口，理论上存在被其它进程抢占的窗口；实践上该窗口
// 极短且测试串行，足以消除「固定端口被长期占用」的确定性冲突。
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("释放空闲端口失败: %v", err)
	}
	return addr
}

// mustPort 从 freeTCPAddr 的 "host:port" 形式取出 ":port" 形式（供契约段 addr 字段使用，
// 该字段按 "host:port" 解析，host 留空表示监听全部网卡）。
func mustPort(t *testing.T, hostPort string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("解析地址 %q 失败: %v", hostPort, err)
	}
	return fmt.Sprintf(":%s", port)
}
