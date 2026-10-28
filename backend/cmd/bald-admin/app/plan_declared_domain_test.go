package app

// plan_declared_domain_test.go —— Domain v2 目标形态的回归锁。
//
// language/dict 用 Domain.Declare 声明节点、Domain.ApplyNode 逐节点执行
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

	cases := []struct {
		domain   string
		key      string
		segments []string
	}{
		{domain: "language", key: "domain.language.biz"},
		{domain: "language", key: "domain.language.routes", segments: []string{"server.http"}},
		{domain: "dict", key: "domain.dict.biz"},
		{domain: "dict", key: "domain.dict.routes", segments: []string{"server.http"}},
	}
	for _, c := range cases {
		n := byKey[appkit.ResourceKey(c.key)]
		require.NotNil(t, n, "%s 域应声明节点 %s", c.domain, c.key)
		assert.False(t, n.Opaque, "%s 应为声明节点（非逃逸舱）", c.key)
		assert.Equal(t, c.segments, n.Segments, "%s 的段", c.key)
		if c.key != "domain."+c.domain+".biz" {
			assert.Contains(t, n.DependsOn, appkit.ResourceKey("domain."+c.domain+".biz"),
				"%s 应依赖域构造节点", c.key)
		}
	}

	// 目标形态：构造已进节点，不再有兼容接线节点；服务注册不经计划。
	for _, dom := range []string{"language", "dict"} {
		assert.Nil(t, byKey[appkit.ResourceKey("domain."+dom+".wire")],
			"%s 已迁 ApplyNode，不应再有兼容接线节点", dom)
	}
	assert.Nil(t, byKey["domain.dict.services"],
		"服务注册走 Domain.Services，不应在计划里声明节点")

	// 这两个域贡献的逃逸舱归零（其余 legacy 域仍是迁移期形态，不在此断言）。
	for _, n := range p.Nodes {
		if !n.Opaque {
			continue
		}
		for _, dom := range []string{"language", "dict"} {
			assert.NotEqual(t, appkit.ResourceKey("domain."+dom), n.Key)
			assert.False(t, strings.HasPrefix(string(n.Key), "domain."+dom+"."),
				"已迁 ApplyNode 的 %s 域不应留下逃逸舱：%s", dom, n.Key)
		}
	}
}
