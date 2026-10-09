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
//     连不上或绑定失败（表现为 `appkit: wait for endpoints canceled: context canceled`
//     ——上层 `cancel()` 打断了 `waitForEndpoints` 的轮询）。
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
	"fmt"
	"net"
	"testing"
)

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
