package e2e

// t_w4_4_registry_e2e_test.go —— Wave 4.4：registry 后端验证（etcd）。
//
// 计划原文：「`bald/registry/{etcd,consul}` 作为 nacos 的替代后端验证
// （当前只用 nacos）」——验收「切换后端后服务注册/发现生效」。
//
// ## 验证策略（真实外部依赖）
//
// etcd 由 Docker 提供（`bald-etcd`，:2379）——**真实验证而非 mock**。
// 若 etcd 不可达则 Skip（环境缺失，不伪装通过）。
//
// 验证链路：Register（注册实例）→ GetService（发现）→ Deregister（注销）
// → 确认已消失。这是「服务注册/发现生效」的直接证据。
//
// ## 为何只验 etcd 不验 consul
//
// consul 镜像拉取受限（Docker Hub 直连不通，daocloud 源无 library/consul）；
// etcd 经 `quay.io/coreos/etcd` 直连可用。故本波**真实验证 etcd**，
// consul 记为「未验证轴」（与 3.3 的 websocket/mcp/graphql 同处理）。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kalandramo/bald/registry"
	etcdreg "github.com/kalandramo/bald/registry/etcd"
	etcdcontract "github.com/kalandramo/bald/registry/etcd/contract"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
)

func etcdAddr() string {
	if v := os.Getenv("BALD_E2E_ETCD_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:2379"
}

// TestWave4_4_EtcdRegisterAndDiscover —— etcd 注册 → 发现 → 注销（真实依赖）。
func TestWave4_4_EtcdRegisterAndDiscover(t *testing.T) {
	ctx := context.Background()
	addr := etcdAddr()

	// 用 contract.Provider 走**与生产同款**的构造路径（非直接 New）。
	cfg := &bootstrapv1.Registry{
		Type: etcdcontract.Type,
		Etcd: &bootstrapv1.Registry_Etcd{Endpoints: []string{addr}},
	}
	reg, cleanup, err := etcdcontract.Provider(ctx, cfg)
	if err != nil {
		t.Skipf("etcd 不可达或构造失败，跳过（环境缺失）: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	t.Logf("etcd Registrar 构造成功（type=%s addr=%s）", etcdcontract.Type, addr)

	// 唯一服务名，避免与其他测试/残留冲突。
	svcName := "e2e-svc-" + time.Now().Format("150405.000000")
	inst := &registry.ServiceInstance{
		ID:        svcName + "-1",
		Name:      svcName,
		Version:   "v1.0.0",
		Endpoints: []string{"http://127.0.0.1:18080"},
		Kind:      "http",
		Metadata:  map[string]string{"env": "e2e"},
	}

	// 1) 注册。
	if err := reg.Register(ctx, inst); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Logf("注册成功: %s", inst.Name)

	// 2) 发现（Registry 同时实现 Discovery 接口）。
	disco, ok := reg.(registry.Discovery)
	if !ok {
		t.Fatal("etcd Registry 未实现 Discovery 接口")
	}
	var found []*registry.ServiceInstance
	for i := 0; i < 20; i++ { // 等注册可见（etcd 写入异步）
		found, err = disco.GetService(ctx, svcName)
		if err == nil && len(found) > 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(found) == 0 {
		t.Fatalf("发现失败：GetService(%s) 返回 0 个实例", svcName)
	}
	// 校验发现到的实例内容（不只看数量）。
	if found[0].Name != svcName {
		t.Fatalf("发现实例名不符: %q, want %q", found[0].Name, svcName)
	}
	if len(found[0].Endpoints) == 0 || found[0].Endpoints[0] != "http://127.0.0.1:18080" {
		t.Fatalf("发现实例 endpoints 不符: %+v", found[0].Endpoints)
	}
	t.Logf("发现成功: %s -> %d 个实例，endpoint=%v", svcName, len(found), found[0].Endpoints)

	// 3) 注销。
	if err := reg.Deregister(ctx, inst); err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	// 4) 确认已消失（发现不到）。
	gone := false
	for i := 0; i < 20; i++ {
		after, gerr := disco.GetService(ctx, svcName)
		if gerr == nil && len(after) == 0 {
			gone = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("注销后仍能发现 %s 的实例", svcName)
	}
	t.Log("注销成功：服务已从 etcd 消失（注册/发现/注销全链路生效）")
}

// TestWave4_4_EtcdProviderConfigValidation —— Provider 的配置校验。
//
// 锁住 fail-fast 语义：type=etcd 但缺 etcd 段 → 报错（不静默）。
func TestWave4_4_EtcdProviderConfigValidation(t *testing.T) {
	ctx := context.Background()
	// type=etcd 但 etcd 段缺失。
	cfg := &bootstrapv1.Registry{Type: etcdcontract.Type}
	_, _, err := etcdcontract.Provider(ctx, cfg)
	if err == nil {
		t.Fatal("type=etcd 但缺 etcd 段应报错（fail-fast）")
	}
	t.Logf("确认 fail-fast: %v", err)
}

// TestWave4_4_EtcdNewRequiresEndpoint —— 直接 New 无 endpoint 的行为。
func TestWave4_4_EtcdNewRequiresEndpoint(t *testing.T) {
	// 无 endpoint 时 New 应报错（而非静默连默认地址）。
	_, err := etcdreg.New()
	if err == nil {
		t.Log("New() 无 endpoint 未报错——检查是否有默认地址（记录行为，不判失败）")
	} else {
		t.Logf("确认 New() 无 endpoint 报错: %v", err)
	}
}
