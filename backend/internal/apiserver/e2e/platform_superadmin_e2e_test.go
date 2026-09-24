package e2e

// platform_superadmin_e2e_test.go —— 平台级身份判据 = superadmin 角色（2026-09-24 定案）。
//
// 验收目标：**同一登录链路，按角色分流**
//   - 持 superadmin 角色的用户（种子 u-admin）→ 登录签发的令牌带 Platform=true
//     → 跨租户可见（平台级视图）
//   - 不持该角色的用户（种子 u-alice / u-bob）→ Platform=false → 仅本租户可见
//
// 与 platform_audit_e2e_test.go 的分工：
//   - 那个文件用手工签发（platformToken）验证**框架能力**（bald pkg/store 的
//     隔离豁免确实生效）——它绕开了业务判据，锁定的是框架层。
//   - 本文件走**真实登录**（POST /v1/login）验证**业务判据接线**——即
//     authmodel.IsPlatformUser 经 SetPlatformResolver 注入后，真的把
//     superadmin 角色变成了令牌里的 Platform=true。锁定的是接线层。
//
// 两者缺一不可：框架能力对了但没接线，或接线对了但框架不认，都测不出问题。

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	apiserver "github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
	securityaudit "github.com/kalandramo/bald-admin/internal/security/audit"
	"github.com/kalandramo/bald/pkg/audit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/contextx"
	ginmw "github.com/kalandramo/bald/pkg/middleware/gin"
	gingonic "github.com/gin-gonic/gin"
)

// startPlatformREST 起真实 REST 引擎，**并按 main.go 同款注入平台判据**。
//
// 为什么不复用 startAuditREST：它构造 Biz 后**未调 SetPlatformResolver**
// （那是对的——它锁定的是框架能力，不需要业务判据）。本文件要测接线，
// 故必须复刻 main.go 的注入。
func startPlatformREST(t *testing.T) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	audit.SetAuditor(securityaudit.NewStore(bootstrappkg.DB))
	t.Cleanup(func() { audit.SetAuditor(audit.NopAuditor()) })

	e := gingonic.New()
	e.Use(ginmw.AuditMiddleware(
		ginmw.AuditWithObjectResolver(authz.DefaultHTTPObject),
		ginmw.AuditWithActionResolver(authz.DefaultHTTPAction),
	))
	auth := authbiz.New(bootstrappkg.Signer)
	// ← 与 cmd/go-bald-admin/main.go 的注入**逐字一致**（生产判据）
	auth.SetPlatformResolver(authmodel.IsPlatformUser)
	apiserver.RegisterRoutes(e, &apiserver.BizSet{
		Auth: auth, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

// seedAuditForTenantP 落一条审计记录到指定租户（唯一 subject 便于精确定位）。
func seedAuditForTenantP(t *testing.T, tenantID, subject string) {
	t.Helper()
	audit.GetAuditor().Record(context.Background(), audit.AuditEvent{
		Subject:  subject,
		TenantID: tenantID,
		Object:   "superadmin-probe",
		Action:   "list",
		Result:   audit.ResultAllow,
	})
}

// TestPlatform_SuperAdminRoleGrantsCrossTenant 是本次判据的**核心验收**：
// 种子 u-admin 持 superadmin 角色 → 真实登录 → 令牌可跨租户查审计。
func TestPlatform_SuperAdminRoleGrantsCrossTenant(t *testing.T) {
	base := startPlatformREST(t)

	const (
		tenantA = "sa-t-a"
		tenantB = "sa-t-b"
	)
	seedAuditForTenantP(t, tenantA, "sa-subject-a")
	seedAuditForTenantP(t, tenantB, "sa-subject-b")

	// 种子数据里 u-admin 的 Roles = "admin,superadmin"（见 bootstrap.go 种子段）。
	if !authmodel.IsPlatformUser(&authmodel.User{Roles: "admin," + authmodel.RoleSuperAdmin}) {
		t.Fatal("前置条件失败：种子 admin 的角色串应被判为平台身份")
	}

	// 真实登录（走 POST /v1/login，经 SetPlatformResolver 判定）。
	tokAdmin := loginAs(t, base, "admin", "admin123")

	// 跨租户可见：admin 属 t-default，却能看到 tenantB 的记录。
	res := listAudit(t, base, tokAdmin, "?subject=sa-subject-b")
	if len(res.Items) != 1 {
		t.Fatalf("superadmin 身份应跨租户看到 B 的记录（1 条），got %d", len(res.Items))
	}
	if res.Items[0].GetTenantId() != tenantB {
		t.Fatalf("跨租户记录租户应为 %s，got %s", tenantB, res.Items[0].GetTenantId())
	}
}

// TestPlatform_PlatformFlagIsTheDecidingFactor 精确隔离「Platform 标记」这一个变量。
//
// 设计动机（一次测试设计纠错）：最初想用 alice（viewer）做「无平台身份」的
// 对照，但实测 403 —— `subject=u-alice, object=audit, action=get` 被拒。
// 原因：casbin 的 p 行里**只有 admin 角色**有 `audit:get/list`
//（bootstrap.go:807-808），viewer 在**授权层**就被拦，走不到租户隔离层。
// 用 alice 对照会同时改变「权限」与「平台身份」两个变量，结论不可归因。
//
// 改用**同 subject、同权限、只差 Platform 标记**的两种令牌：
//   - 真实登录（DB 里 u-admin 有 superadmin 角色）→ Platform=true → 跨租户可见
//   - 手工签发（tenantToken，不带 Platform）→ Platform=false → 隔离生效
//
// 后者能通过 casbin（subject=u-admin 命中 `g, u-admin, admin` 行，而 g 行由
// DB 的 User.Roles 装载，与 token 的 Platform 无关），故两例只差一个变量。
//
// 这同时锁定一条重要语义：**跨租户能力来自令牌的 Platform 标记本身，
// 不是来自「u-admin 这个用户名/角色」**——即使该用户在 DB 里持有 superadmin，
// 一个不带标记的令牌仍被隔离。这正是「显式声明、绝不隐式推断」的体现。
func TestPlatform_PlatformFlagIsTheDecidingFactor(t *testing.T) {
	base := startPlatformREST(t)

	const (
		tenantA = "pfd-t-a"
		tenantB = "pfd-t-b"
	)
	seedAuditForTenantP(t, tenantA, "pfd-subject-a")
	seedAuditForTenantP(t, tenantB, "pfd-subject-b")

	// 例 1：真实登录（判据生效）→ Platform=true → 跨租户可见。
	tokLogin := loginAs(t, base, "admin", "admin123")
	resLogin := listAudit(t, base, tokLogin, "?subject=pfd-subject-b")
	if len(resLogin.Items) != 1 {
		t.Fatalf("superadmin 真实登录应跨租户可见（1 条），got %d", len(resLogin.Items))
	}

	// 例 2：手工签发（无 Platform 标记，同 subject/同权限）→ 隔离生效。
	tokManual := tenantToken(t, "admin", "u-admin", "admin", tenantA)
	resManual := listAudit(t, base, tokManual, "?subject=pfd-subject-b")
	if len(resManual.Items) != 0 {
		t.Fatalf("无 Platform 标记的令牌不应跨租户可见，got %d 条"+
			"（若可见，说明隔离被绕过——跨租户能力不应来自 subject 的角色）", len(resManual.Items))
	}
	// 但本租户记录仍可见（隔离 ≠ 全禁；也证明该令牌确实通过了授权）。
	resOwn := listAudit(t, base, tokManual, "?subject=pfd-subject-a")
	if len(resOwn.Items) != 1 {
		t.Fatalf("无 Platform 标记的令牌应能看到本租户记录（1 条），got %d"+
			"（若为 0，说明请求在授权层就被拒了，本对照不成立）", len(resOwn.Items))
	}
}

// TestPlatform_ResolverIsInjectedInProduction 锁定「生产真的注入了判据」。
//
// 防的是「判据写好了但 main.go 忘了调 SetPlatformResolver」——那样
// superadmin 角色形同虚设，而上面两条测试若各自手工注入则测不出来。
//
// 实现方式：直接读 main.go 的源码文本断言注入语句存在。这比「跑 main」
// 轻量得多（起完整进程需要 Redis/DB），且**能抓住**删除注入行的回归。
func TestPlatform_ResolverIsInjectedInProduction(t *testing.T) {
	src, err := os.ReadFile("../../../cmd/go-bald-admin/main.go")
	if err != nil {
		t.Fatalf("读 main.go: %v", err)
	}
	want := "bizSet.Auth.SetPlatformResolver(authmodel.IsPlatformUser)"
	if !strings.Contains(string(src), want) {
		t.Fatalf("main.go 缺少生产判据注入：期望含 %q\n"+
			"（删掉它会让 superadmin 角色失效——令牌不再带 Platform=true）", want)
	}
}

// TestPlatform_ClaimsCarryPlatformFlag 锁定判据 → 令牌 claims 的映射链路：
// 注入判据后，登录签发的令牌解析出的 claims.Platform 必须为 true。
//
// 这是对 authn-jwt 字段集缺口的**业务层回归**（该缺口曾让 Platform 经 JWT
// 静默丢失——token 以 true 签发、解析后变 false）。框架层已有反射比对测试
// （contrib/authn-jwt 的 TestJWTClaims_FieldSetSync），本用例锁定端到端。
func TestPlatform_ClaimsCarryPlatformFlag(t *testing.T) {
	base := startPlatformREST(t)
	tok := loginAs(t, base, "admin", "admin123")

	claims, err := bootstrappkg.Authenticator.AuthenticateToken(tok)
	if err != nil {
		t.Fatalf("验证令牌: %v", err)
	}
	if !claims.Platform {
		t.Fatal("superadmin 登录签发的令牌，claims.Platform 应为 true" +
			"（若为 false，说明 Platform 在签发或解析链路上丢失）")
	}
	// 且 ctx 层确实被标记（pkg/store 据此跳过隔离）。
	ctx := authn.ContextWithAuthClaims(context.Background(), claims)
	if !contextx.PlatformFromContext(ctx) {
		t.Fatal("claims.Platform=true 时应写入 ctx 平台标记")
	}

	// 对照：alice 的令牌不带该标记。
	tokAlice := loginAs(t, base, "alice", "alice123")
	claimsA, err := bootstrappkg.Authenticator.AuthenticateToken(tokAlice)
	if err != nil {
		t.Fatalf("验证 alice 令牌: %v", err)
	}
	if claimsA.Platform {
		t.Fatal("非 superadmin 的令牌不应带 Platform 标记")
	}
}
