package e2e

// t_w1_7_org_validation_e2e_test.go —— Wave 校验层契约化：org 域的端到端校验验证。
//
// 验证三件事：
//   A. **gRPC 面**：buf.validate 注解经 ValidatorInterceptor 生效——空 code →
//      InvalidArgument（非 500/403），合法值 → 成功。
//   B. **gin 面**：同一份注解经 handler 的 validatePB 生效——空 code → 400。
//   C. **Update 语义回归**：UpdateOrgUnitRequest 的 name 为空 = 不改，
//      **不得**被 min_len 拒（这是 Create/Update 的关键差异，写错会拒绝正常操作）。
//
// 校验规则的**单一真源**是 proto 注解；本文件不重复断言规则文本，只断言
// 运行时行为——规则若被改错，这里会红。

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/kalandramo/bald/pkg/authz"
	grpcmw "github.com/kalandramo/bald/pkg/middleware/grpc"
	grpcserver "github.com/kalandramo/bald/transport/grpc"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	secretgrpc "github.com/kalandramo/bald-admin/internal/apiserver/handler/grpc"
	validation "github.com/kalandramo/bald-admin/internal/security/validation"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// annotationErrMarker 是注解层错误消息的特征串（internal/security/validation
// 的 mapValidationError 产出）。用于区分「注解层拒绝」与「biz 兜底拒绝」——
// 二者状态码相同（都 400），只有 message 能证明是校验层生效。
const annotationErrMarker = "请求参数校验失败"

// startOrgValidationGRPCServer 起一个**含校验层**的真实 gRPC server。
//
// 与 t_w1_7_org_grpc_e2e_test.go 的 startOrgGRPCServer 的差异：链尾多了
// ValidatorInterceptor——这正是本文件要验证的层。链序与生产
// （main.go newGRPCServerOptions）一致：Error → Authn → Authz → Validator。
func startOrgValidationGRPCServer(t *testing.T) (string, func()) {
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
	validatorI := grpcmw.ValidatorInterceptor(validation.MustNew().Validate)

	srv := grpcserver.NewGRPCServerWithRegister(
		&bootstrapv1.Server_Grpc{Addr: ":0"},
		[]grpc.ServerOption{grpc.ChainUnaryInterceptor(
			grpcmw.ErrorInterceptor(), authnI, authzI, validatorI)},
		func(s *grpc.Server) {
			identityv1.RegisterOrgUnitServiceServer(s, secretgrpc.NewOrgServer(orgbiz.New(bootstrappkg.OrgUnitStore, bootstrappkg.PositionStore, bootstrappkg.UserStore)))
			identityv1.RegisterPositionServiceServer(s, secretgrpc.NewPositionServer(orgbiz.New(bootstrappkg.OrgUnitStore, bootstrappkg.PositionStore, bootstrappkg.UserStore)))
		},
	)
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() { srv.Stop(context.Background()) }
}

// ---- A. gRPC 面 ----

// TestOrgValidation_GRPC_EmptyCodeRejected 空 code 应 InvalidArgument（注解 min_len:1）。
//
// **注意断言的是 message 来源**（非仅状态码）：biz 层也有非空兜底（org.go:123
// 的 "name and code required"），只断言 InvalidArgument 会被 biz 掩盖成恒真——
// 校验层失效时测试仍绿。故断言 message 含注解层特征串（"请求参数校验失败"），
// 确保测的是 ValidatorInterceptor 而非 biz 兜底。
func TestOrgValidation_GRPC_EmptyCodeRejected(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: "", Name: "财务部"},
		new(identityv1.CreateOrgUnitResponse))
	got := status.Code(err).String()
	if got != "InvalidArgument" {
		t.Fatalf("空 code: want InvalidArgument, got %v (%v)", got, err)
	}
	// 断言错误来自注解层（biz 兜底的 message 是 "name and code required"）。
	if !strings.Contains(err.Error(), annotationErrMarker) {
		t.Fatalf("错误应来自注解层（含 %q），实得: %v（说明被 biz 兜底掩盖，校验层可能未生效）",
			annotationErrMarker, err)
	}
	t.Logf("空 code 被注解层拒绝 ✅ status=%s msg=%v", got, err)
}

// TestOrgValidation_GRPC_EmptyNameRejected 空 name 应被**注解层**拒绝。
func TestOrgValidation_GRPC_EmptyNameRejected(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: "V-1", Name: ""},
		new(identityv1.CreateOrgUnitResponse))
	if status.Code(err).String() != "InvalidArgument" {
		t.Fatalf("空 name: want InvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), annotationErrMarker) {
		t.Fatalf("错误应来自注解层，实得: %v", err)
	}
}

// TestOrgValidation_GRPC_CodeTooLongRejected 超长 code（65 > max_len:64）被拒。
func TestOrgValidation_GRPC_CodeTooLongRejected(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: string(long), Name: "x"},
		new(identityv1.CreateOrgUnitResponse))
	if status.Code(err).String() != "InvalidArgument" {
		t.Fatalf("超长 code: want InvalidArgument, got %v", err)
	}
}

// TestOrgValidation_GRPC_LegalAccepted 合法入参应成功（校验不误伤正常请求）。
func TestOrgValidation_GRPC_LegalAccepted(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	resp := new(identityv1.CreateOrgUnitResponse)
	if err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{
			Code: "V-OK-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10), Name: "合法组织",
			Type: identityv1.OrgUnit_TYPE_COMPANY,
		}, resp); err != nil {
		t.Fatalf("合法入参应成功，got %v", err)
	}
	if resp.GetOrgUnit().GetCode() == "" {
		t.Fatal("响应应含创建的 code")
	}
}

// TestOrgValidation_GRPC_UpdateEmptyNameAllowed Update 语义回归（**关键**）。
//
// UpdateOrgUnitRequest.name 为空 = 不改，注解只加了 max_len 未加 min_len——
// 若误加 min_len，这里会 InvalidArgument（正常操作被拒）。
func TestOrgValidation_GRPC_UpdateEmptyNameAllowed(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)
	tok := orgGRPCToken(t, "admin", "u-admin", "admin")

	code := "V-UPD-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10)
	// 先建一个。
	if err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: code, Name: "原名"},
		new(identityv1.CreateOrgUnitResponse)); err != nil {
		t.Fatalf("setup create: %v", err)
	}
	// 只改 remark，不传 name —— 应成功（name 空 = 不改）。
	if err := conn.Invoke(grpcOutCtx(tok),
		"/go.bald.admin.identity.v1.OrgUnitService/UpdateOrgUnit",
		&identityv1.UpdateOrgUnitRequest{Code: code, Remark: "只改备注"},
		new(identityv1.UpdateOrgUnitResponse)); err != nil {
		t.Fatalf("Update 空 name（=不改）应成功，got %v", err)
	}
	t.Log("Update 空 name 放行 ✅（Create/Update 语义差异正确）")
}

// TestOrgValidation_GRPC_NoTokenStillUnauthenticated 校验层在 Authz 之后——
// 未认证请求仍应 Unauthenticated（不得因校验层前移而泄漏校验规则）。
func TestOrgValidation_GRPC_NoTokenStillUnauthenticated(t *testing.T) {
	addr, stop := startOrgValidationGRPCServer(t)
	defer stop()
	conn := orgGRPCConn(t, addr)

	err := conn.Invoke(grpcOutCtx(""),
		"/go.bald.admin.identity.v1.OrgUnitService/CreateOrgUnit",
		&identityv1.CreateOrgUnitRequest{Code: "", Name: ""},
		new(identityv1.CreateOrgUnitResponse))
	if status.Code(err).String() != "Unauthenticated" {
		t.Fatalf("无 token: want Unauthenticated（校验层应在 Authz 之后），got %v", err)
	}
}

// ---- B. gin 面 ----

// TestOrgValidation_Gin_EmptyCodeRejected gin 面空 code → 400，且错误来自**注解层**。
//
// gin 直连路径不经过 gRPC 拦截器链，校验由 handler 的 validatePB 触发——
// 本用例锁定该接线。断言 message 含注解层特征串（reason 两侧都是
// INVALID_ARGUMENT，无法区分；见 annotationErrMarker 说明）。
func TestOrgValidation_Gin_EmptyCodeRejected(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "财务部", "code": "", "type": "TYPE_COMPANY"})
	if code != http.StatusBadRequest {
		t.Fatalf("空 code: want 400, got %d body=%s", code, raw)
	}
	if !strings.Contains(string(raw), annotationErrMarker) {
		t.Fatalf("错误应来自注解层（含 %q），实得: %s（说明被 biz 兜底掩盖）",
			annotationErrMarker, raw)
	}
	t.Logf("gin 空 code 被注解层拒绝 ✅ status=%d", code)
}

// TestOrgValidation_Gin_CodeTooLongRejected gin 面超长 code → 400。
//
// 长度校验 biz 不检查——只有注解层能拦，故此用例**唯一**锁定 gin 侧校验接线
// （空值用例会被 biz 兜底掩盖）。
func TestOrgValidation_Gin_CodeTooLongRejected(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "财务部", "code": string(long), "type": "TYPE_COMPANY"})
	if code != http.StatusBadRequest {
		t.Fatalf("超长 code: want 400（注解层 max_len），got %d body=%s", code, raw)
	}
	if !strings.Contains(string(raw), annotationErrMarker) {
		t.Fatalf("错误应来自注解层，实得: %s", raw)
	}
	t.Logf("gin 超长 code 被注解层拒绝 ✅ status=%d", code)
}

// TestOrgValidation_Gin_LegalAccepted gin 面合法入参成功。
func TestOrgValidation_Gin_LegalAccepted(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "合法组织", "code": "v-gin-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10), "type": "TYPE_COMPANY"})
	if code != http.StatusCreated {
		t.Fatalf("合法入参: want 201, got %d body=%s", code, raw)
	}
}

// TestOrgValidation_Gin_UpdateEmptyNameAllowed gin 面 Update 语义回归。
func TestOrgValidation_Gin_UpdateEmptyNameAllowed(t *testing.T) {
	base := startOrgREST(t)
	admin := loginAs(t, base, "admin", "admin123")

	code := "v-ginupd-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10)
	if c, raw := callRaw(t, base, admin, http.MethodPost, "/v1/org-units",
		map[string]any{"name": "原名", "code": code, "type": "TYPE_COMPANY"}); c != http.StatusCreated {
		t.Fatalf("setup: %d %s", c, raw)
	}
	// 只改 remark，不传 name。
	if c, raw := callRaw(t, base, admin, http.MethodPut, "/v1/org-units/"+code,
		map[string]any{"remark": "只改备注"}); c != http.StatusOK {
		t.Fatalf("Update 空 name 应成功，got %d body=%s", c, raw)
	}
}
