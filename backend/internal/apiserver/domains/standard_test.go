package domains

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalandramo/bald/pkg/appkit"
)

func TestStandardDomain_DeclaresRoutesOnlyWithHTTPFace(t *testing.T) {
	dom := StandardDomain("greet", "greetd",
		func(context.Context, *appkit.App) error { return nil },
		func(http.Handler) error { return nil },
	)
	require.Equal(t, "greet", dom.Name)

	scope := appkit.PlanScope{Decl: &appkit.Decl{}}
	nodes, err := dom.Declare(context.Background(), scope)
	require.NoError(t, err)
	require.Len(t, nodes, 1, "无 HTTP 面时只声明 biz 节点")
	assert.Equal(t, appkit.ResourceKey("domain.greet.biz"), nodes[0].Key)
	assert.Equal(t, appkit.PhaseWire, nodes[0].Phase)

	scope.Decl.HTTPHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	nodes, err = dom.Declare(context.Background(), scope)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, appkit.ResourceKey("domain.greet.routes"), nodes[1].Key)
	assert.Equal(t, []string{"server.http"}, nodes[1].Segments)
	assert.Equal(t, appkit.PhaseServe, nodes[1].Phase)
	assert.Contains(t, nodes[1].DependsOn, appkit.ResourceKey("domain.greet.biz"))
}

func TestStandardDomain_ApplyNodeDispatches(t *testing.T) {
	var builtErr error
	var mounted http.Handler
	dom := StandardDomain("greet", "greetd",
		func(context.Context, *appkit.App) error { return builtErr },
		func(h http.Handler) error { mounted = h; return nil },
	)
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	app := appkit.NewApp(appkit.WithHTTP(h))
	ctx := context.Background()

	require.NoError(t, dom.ApplyNode(ctx, app, appkit.Node{Key: "domain.greet.biz"}))
	require.NoError(t, dom.ApplyNode(ctx, app, appkit.Node{Key: "domain.greet.routes"}))
	require.NotNil(t, mounted, "routes 节点应拿到装配声明的业务 handler")
	assert.Equal(t, reflect.ValueOf(h).Pointer(), reflect.ValueOf(mounted).Pointer(),
		"routes 节点拿到的应是 WithHTTP 声明的那个 handler")

	builtErr = errors.New("boom")
	assert.ErrorIs(t, dom.ApplyNode(ctx, app, appkit.Node{Key: "domain.greet.biz"}), builtErr)

	err := dom.ApplyNode(ctx, app, appkit.Node{Key: "domain.greet.other"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greetd: unknown declared node")
}
