package authd

import (
	"context"
	"net/http"
	"testing"

	"github.com/kalandramo/bald/pkg/appkit"
)

// TestDomain_DeclaresReconcileBizNode 钉住 auth 域的热更声明面：
// biz 节点读取 biz.login 且标记 ReloadReconcile（Q8 的消费侧），routes 节点
// 仅在声明了 HTTP 面时投影并依赖 biz 节点。
func TestDomain_DeclaresReconcileBizNode(t *testing.T) {
	dom := Domain(Deps{})

	nodes, err := dom.Declare(context.Background(), appkit.PlanScope{Decl: &appkit.Decl{}})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Key != bizKey {
		t.Fatalf("nodes without HTTP face = %+v, want only %q", nodes, bizKey)
	}
	if nodes[0].Reload != appkit.ReloadReconcile {
		t.Fatalf("biz node reload = %v, want ReloadReconcile", nodes[0].Reload)
	}
	if len(nodes[0].Segments) != 1 || nodes[0].Segments[0] != "biz.login" {
		t.Fatalf("biz node segments = %v, want [biz.login]", nodes[0].Segments)
	}

	nodes, err = dom.Declare(context.Background(), appkit.PlanScope{Decl: &appkit.Decl{HTTPHandler: http.NewServeMux()}})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if len(nodes) != 2 || nodes[1].Key != routesKey {
		t.Fatalf("nodes with HTTP face = %+v, want biz+routes", nodes)
	}
	if len(nodes[1].DependsOn) != 1 || nodes[1].DependsOn[0] != bizKey {
		t.Fatalf("routes deps = %v, want [%q]", nodes[1].DependsOn, bizKey)
	}
}
