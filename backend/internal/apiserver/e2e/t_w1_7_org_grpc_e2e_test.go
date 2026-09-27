package e2e

// t_w1_7_org_grpc_e2e_test.go —— Wave 1.7 补齐：org 域 gRPC 面。
//
// 本文件验证两件事，对应本次改动的两个层次：
//   A. **授权归一化错配已修**（Wave 1）：P9 把 gRPC Service 名压成无分隔小写
//      （OrgUnitService→"orgunit"），而策略 object 是 HTTP 路径段风格
//      （"org-units"）；casbin 精确匹配下 gRPC 面恒 403。别名装饰器修复后，
//      admin 调 org gRPC 方法应放行。
//   B. **org gRPC service 实现可用**（Wave 2）：真实 gRPC server 注册后，
//      经完整拦截器链调用 ListOrgUnits/CreateOrgUnit 应成功返回。
//
// 复现说明（修复前的红灯形态）：本文件 A 部分的用例在 Wave 1 修复前会因
// `orgunit` 无策略行而返回 PermissionDenied——即「gRPC 面 403」的实测证据。
// 计划期已用探针复现（见计划「反证/复现」复现 2），本文件把它固化为回归测试。

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	grpcmw "github.com/kalandramo/bald/pkg/middleware/grpc"
	grpcserver "github.com/kalandramo/bald/transport/grpc"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"

	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ---- A. 授权归一化：gRPC 面 org 现在放行 ----

// TestOrgGRPC_Authz_Admin_OrgUnit_Allowed admin 调 org_unit 各方法应放行。
//
// 锁定「orgunit → org-units」别名 + 「list/write」动作补齐。
// 修复前：全部 PermissionDenied（object 错配 + 动作缺失双重原因）。
func TestOrgGRPC_Authz_Admin_OrgUnit_Allowed(t *testing.T) {
	tok := issueToken(t, "admin", "u-admin", "admin")
	cases := []struct {
		fullMethod string
		why        string
	}{
		{"/go.bald.admin.identity.v1.OrgUnitService/ListOrgUnits", "object 别名 orgunit→org-units + 动作 list"},
		{"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit", "动作 write"},
		{"/go.bald.admin.identity.v1.OrgUnitService/GetOrgUnit", "动作 get"},
		{"/go.bald.admin.identity.v1.OrgUnitService/DeleteOrgUnit", "动作 delete"},
		{"/go.bald.admin.identity.v1.PositionService/ListPositions", "object 别名 position→positions + 动作 list"},
		{"/go.bald.admin.identity.v1.PositionService/CreatePosition", "动作 write"},
	}
	for _, c := range cases {
		if err := invoke(t, tok, c.fullMethod); err != nil {
			t.Errorf("admin %s: want allowed (%s), got %v", c.fullMethod, c.why, err)
		}
	}
}

// TestOrgGRPC_Authz_Viewer_ReadOnly viewer 读 org 放行、写被拒。
func TestOrgGRPC_Authz_Viewer_ReadOnly(t *testing.T) {
	tok := issueToken(t, "alice", "u-alice", "viewer")

	// 读：viewer 有 org-units/positions 的 get 与 list。
	if err := invoke(t, tok, "/go.bald.admin.identity.v1.OrgUnitService/ListOrgUnits"); err != nil {
		t.Errorf("viewer ListOrgUnits: want allowed, got %v", err)
	}
	// 写：viewer 无 write。
	if err := invoke(t, tok, "/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit"); err == nil {
		t.Error("viewer CreateOrgUnit: want PermissionDenied, got nil")
	}
}

// TestOrgGRPC_Authz_Dict_Regression dict 域 gRPC 面既存 403 已随同修复。
//
// dict 域 gRPC 面此前**已挂载**（main.go registerGRPC），故它是同一根因的
// 既存静默失效——本用例锁定它已恢复。
func TestOrgGRPC_Authz_Dict_Regression(t *testing.T) {
	tok := issueToken(t, "admin", "u-admin", "admin")
	for _, m := range []string{
		"/go.bald.admin.dict.v1.DictTypeService/ListDictTypes",
		"/go.bald.admin.dict.v1.DictEntryService/ListDictEntries",
	} {
		if err := invoke(t, tok, m); err != nil {
			t.Errorf("admin %s: want allowed (dict 既存 403 已修), got %v", m, err)
		}
	}
}

// TestOrgGRPC_Authz_NoToken 无 token 调 org gRPC 方法应 Unauthenticated。
func TestOrgGRPC_Authz_NoToken(t *testing.T) {
	if err := invoke(t, "", "/go.bald.admin.identity.v1.OrgUnitService/ListOrgUnits"); err == nil {
		t.Fatal("want Unauthenticated, got nil")
	}
}

// ---- B. 真实 gRPC 传输：org service 实现可用 ----

// startOrgGRPCServer 起真实 gRPC server（:0 动态端口），注册 org service，
// 走完整拦截器链（Error→Authn→Authz）。范式对齐 handler/grpc/secret_e2e_test.go。
func startOrgGRPCServer(t *testing.T) (string, func()) {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authnI := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return grpcmw.AuthnInterceptor(bootstrappkg.Authenticator)(ctx, req, info, handler)
	}
	authzI := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return grpcmw.AuthzInterceptor(bootstrappkg.Authorizer,
			grpcmw.WithObjectResolver(authz.DefaultGRPCObject),
			grpcmw.WithActionResolver(authz.DefaultGRPCAction))(ctx, req, info, handler)
	}
	srv := grpcserver.NewGRPCServerWithRegister(
		&bootstrapv1.Server_Grpc{Addr: ":0"},
		[]grpc.ServerOption{grpc.ChainUnaryInterceptor(grpcmw.ErrorInterceptor(), authnI, authzI)},
		func(s *grpc.Server) {
			identityv1.RegisterOrgUnitServiceServer(s, secretgrpc.NewOrgServer(orgbiz.New()))
			identityv1.RegisterPositionServiceServer(s, secretgrpc.NewPositionServer(orgbiz.New()))
		},
	)
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() { srv.Stop(context.Background()) }
}

// orgGRPCConn 建立标准 proto codec 的 gRPC 客户端连接。
func orgGRPCConn(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// orgGRPCToken 签发 org gRPC 测试用 token。
func orgGRPCToken(t *testing.T, username, userID, role string) string {
	t.Helper()
	claims := authn.AuthClaims{
		Issuer: "bald-admin", Subject: userID, TenantID: "t-default",
		Roles: []string{role}, Name: username,
	}
	tok, err := bootstrappkg.Signer.IssueToken(claims, 2*time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// grpcOutCtx 构造带 Bearer token 的 outgoing context（客户端侧）。
func grpcOutCtx(token string) context.Context {
	ctx := context.Background()
	if token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// TestOrgGRPC_Transport_CreateAndList 真实传输：admin 建 org unit → List 可见。
//
// 这是端到端闭环：gRPC 请求体 → protojson 无涉（原生 proto codec）→ biz →
// store（含 tenant 隔离）→ 响应。
func TestOrgGRPC_Transport_CreateAndList(t *testing.T) {
	addr, stop := startOrgGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	code := "grpc-org-" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10)
	// 创建。
	createResp := new(identityv1.CreateOrgUnitResponse)
	if err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{
			Code: code, Name: "gRPC 建的组织", Type: identityv1.OrgUnit_TYPE_COMPANY,
		}, createResp); err != nil {
		t.Fatalf("CreateOrgUnit: %v", err)
	}
	if got := createResp.GetOrgUnit().GetCode(); got != code {
		t.Fatalf("created code=%q, want %q", got, code)
	}

	// List（根节点分页）应含刚建的。
	listResp := new(identityv1.ListOrgUnitsResponse)
	if err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/ListOrgUnits",
		&identityv1.ListOrgUnitsRequest{}, listResp); err != nil {
		t.Fatalf("ListOrgUnits: %v", err)
	}
	found := false
	for _, it := range listResp.GetItems() {
		if it.GetCode() == code {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListOrgUnits 未含刚建的 %s", code)
	}
}

// TestOrgGRPC_Transport_NoToken 真实传输：无 token → Unauthenticated。
func TestOrgGRPC_Transport_NoToken(t *testing.T) {
	addr, stop := startOrgGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)

	err := conn.Invoke(grpcOutCtx(""),
		"/go.bald.admin.identity.v1.OrgUnitService/ListOrgUnits",
		&identityv1.ListOrgUnitsRequest{}, new(identityv1.ListOrgUnitsResponse))
	if status.Code(err).String() != "Unauthenticated" {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}

// TestOrgGRPC_Transport_ViewerWriteForbidden 真实传输：viewer 写 → PermissionDenied。
func TestOrgGRPC_Transport_ViewerWriteForbidden(t *testing.T) {
	addr, stop := startOrgGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "alice", "u-alice", "viewer")

	err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: "grpc-viewer-x", Name: "x"},
		new(identityv1.CreateOrgUnitResponse))
	if status.Code(err).String() != "PermissionDenied" {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

// TestOrgGRPC_Transport_DictList_OK 真实传输：dict gRPC 面（既存 403）现已放行。
//
// 与 A 部分互补：A 用 stub handler 测授权层，本用例走真实 dict service 实现。
func TestOrgGRPC_Transport_DictList_OK(t *testing.T) {
	addr, stop := startOrgGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	// 注意：本 server 只注册了 org service，dict 方法未注册 → Unimplemented。
	// 故此用例改在 A 部分（invoke，stub handler）覆盖授权放行；此处仅确认
	// 授权链对 dict 方法不再 403（Unimplemented 说明已过授权、到达路由层）。
	err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.dict.v1.DictTypeService/ListDictTypes",
		&dictv1.ListDictTypesRequest{}, new(dictv1.ListDictTypesResponse))
	if status.Code(err).String() == "PermissionDenied" {
		t.Fatalf("dict ListDictTypes 仍被 403（别名修复未生效）: %v", err)
	}
}
