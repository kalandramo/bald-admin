package app

// plan_declared_domain_test.go —— Domain v2 声明面的回归锁。
//
// language 域是 bald-admin 首个用 Domain.Declare 声明拓扑的域（bald 设计
// §4.6 迁移期形态）：路由从「整块 Opaque」变成计划里的具名节点（段与依赖
// 可检视），实际构造仍收在兼容接线节点 domain.language.wire。本测试走生产
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

func TestPlan_LanguageDomainDeclaresRoutes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "bald-admin.yaml"),
		[]byte(sqliteTestConfigFor("127.0.0.1:0", "127.0.0.1:0")), 0o644))
	t.Chdir(dir)

	opts := append(appOptions(options.NewServerOptions()), appkit.ConfigFile("configs/bald-admin.yaml"))
	p, err := appkit.Compile(context.Background(), opts...)
	require.NoError(t, err)

	var routes, compat *appkit.Node
	for i := range p.Nodes {
		switch p.Nodes[i].Key {
		case "domain.language.routes":
			routes = &p.Nodes[i]
		case "domain.language.wire":
			compat = &p.Nodes[i]
		}
	}

	require.NotNil(t, routes, "language 域应声明 domain.language.routes")
	assert.False(t, routes.Opaque, "声明节点不应是逃逸舱")
	assert.Equal(t, []string{"server.http"}, routes.Segments)
	assert.Contains(t, routes.DependsOn, appkit.ResourceKey("domain.language.wire"))

	require.NotNil(t, compat, "兼容接线应收在 domain.language.wire")
	assert.True(t, compat.Opaque, "迁移期构造仍不透明")
}
