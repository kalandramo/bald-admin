package app

// plan_declared_domain_test.go —— Domain v2 目标形态的回归锁。
//
// 20 个域都用 Domain.Declare 声明节点、Domain.ApplyNode 逐节点执行
//（bald 设计 §4.6）：biz 节点构造领域能力，routes 节点挂 REST 路由；服务
// 注册仍走 Domain.Services（server 构建时的延迟回调），域不再产生 Opaque
// 兼容节点。本测试走生产装配声明（appOptions）编译计划，锁住这几点——若
// Declare/ApplyNode 被删或节点语义回退，计划里就看得到。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald/pkg/appkit"

	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
)

func TestPlan_DeclaredDomains(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(sqliteTestConfigFor("127.0.0.1:0", "127.0.0.1:0")), 0o644))
	t.Chdir(dir)

	opts := append(appOptions(options.NewServerOptions()), appkit.ConfigFile("configs/bald-admin.yaml"))
	p, err := appkit.Compile(context.Background(), opts...)
	require.NoError(t, err)

	byKey := make(map[appkit.ResourceKey]*appkit.Node, len(p.Nodes))
	for i := range p.Nodes {
		byKey[p.Nodes[i].Key] = &p.Nodes[i]
	}

	// 全部域同形：biz 节点恒有，routes 节点随 HTTP 面投影（生产装配声明了
	// gin handler，故 20 个域都有路由节点）。
	domains := []string{
		"auditlog", "auth", "cachemonitor", "dashboard", "dict", "file",
		"identity", "language", "menu", "message", "mfa", "org", "permgroup",
		"permission", "plan", "portal", "secret", "task", "tenant", "user",
	}
	for _, dom := range domains {
		bizKey := appkit.ResourceKey("domain." + dom + ".biz")
		biz := byKey[bizKey]
		require.NotNil(t, biz, "%s 域应声明 biz 节点", dom)
		assert.False(t, biz.Opaque, "%s 的 biz 节点应是声明节点（非逃逸舱）", dom)

		routes := byKey[appkit.ResourceKey("domain."+dom+".routes")]
		require.NotNil(t, routes, "%s 域应声明 routes 节点", dom)
		assert.False(t, routes.Opaque, "%s 的 routes 节点应是声明节点（非逃逸舱）", dom)
		assert.Equal(t, []string{"server.http"}, routes.Segments, "%s routes 的段", dom)
		assert.Contains(t, routes.DependsOn, bizKey, "%s 的 routes 应依赖 biz 节点", dom)

		// 迁移期形态已收口：没有整块兼容接线节点，服务注册不经计划。
		assert.Nil(t, byKey[appkit.ResourceKey("domain."+dom+".wire")],
			"%s 已迁 ApplyNode，不应再有兼容接线节点", dom)
		assert.Nil(t, byKey[appkit.ResourceKey("domain."+dom+".services")],
			"%s 的服务注册走 Domain.Services，不应在计划里声明节点", dom)
	}

	// 域不再贡献任何逃逸舱：Opaque 只应来自框架 Provider。
	for _, n := range p.Nodes {
		if !n.Opaque {
			continue
		}
		assert.NotEqual(t, appkit.OriginDomain, n.Origin,
			"域不应留下逃逸舱：%s", n.Key)
		assert.False(t, strings.HasPrefix(string(n.Key), "domain."),
			"域不应留下逃逸舱：%s", n.Key)
	}
}
