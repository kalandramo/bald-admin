package app

// plan_declared_domain_test.go —— Domain v2 声明面的回归锁。
//
// language/dict 是 bald-admin 首批用 Domain.Declare 声明拓扑的域（bald 设计
// §4.6 迁移期形态）：路由/服务从「整块 Opaque」变成计划里的具名节点（段与
// 依赖可检视），实际构造仍收在兼容接线节点 domain.<name>.wire。本测试走生产
// 装配声明（appOptions）编译计划，锁住这两半——若 Declare 被删或节点语义
// 回退，计划里就看得到。

import (
	"context"
	"os"
	"path/filepath"
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

	cases := []struct {
		domain   string
		key      string
		segments []string
	}{
		{domain: "language", key: "domain.language.routes", segments: []string{"server.http"}},
		{domain: "dict", key: "domain.dict.routes", segments: []string{"server.http"}},
		{domain: "dict", key: "domain.dict.services", segments: []string{"server.grpc"}},
	}
	for _, c := range cases {
		n := byKey[appkit.ResourceKey(c.key)]
		require.NotNil(t, n, "%s 域应声明节点 %s", c.domain, c.key)
		assert.False(t, n.Opaque, "%s 应为声明节点（非逃逸舱）", c.key)
		assert.Equal(t, c.segments, n.Segments, "%s 的段", c.key)
		assert.Contains(t, n.DependsOn, appkit.ResourceKey("domain."+c.domain+".wire"),
			"%s 应依赖兼容接线节点", c.key)
	}

	// 兼容接线节点承接实际构造：仍为 Opaque，且不再逐槽位投影。
	for _, dom := range []string{"language", "dict"} {
		compat := byKey[appkit.ResourceKey("domain."+dom+".wire")]
		require.NotNil(t, compat, "%s 的兼容接线应收在 domain.%s.wire", dom, dom)
		assert.True(t, compat.Opaque, "%s 迁移期构造仍不透明", dom)
	}
}
