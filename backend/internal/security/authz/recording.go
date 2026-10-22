// recording.go 授权判定记录装饰器——把每次策略评估落一条「策略评估日志」。
//
// 解决的问题：permgroup 域的读面（ListEvalLogs/GetEvalLog +
// GET /v1/policy-evaluation-logs）、表与 AutoMigrate、仓储早已全量就位，
// 写入方 Biz.RecordEvaluation 却从未接进判定链路（其注释自陈「本项目当前未在
// authz 中间件里接线」）——端点恒返回空 items。本装饰器就是那个缺失的生产写入方。
//
// 为什么做成装饰器而不是改中间件：authz.Authorizer 只有 Authorize 一个方法
// （bald pkg/authz/authz.go:15-20），contrib/authz-casbin 亦无评估回调；
// bald 是零 replace 的外部 module，框架侧不可改。旁路记录只能在应用层包一层
// ——与同目录 ReloadableAuthorizer / ObjectAliasAuthorizer 同款手法。
//
// ## 字段取法（ctx 里到底有什么）
//
// 框架把 c.Request.Context()（gin，pkg/middleware/gin/authz.go:82）或 gRPC
// handler ctx（pkg/middleware/grpc/authz.go:92）传给 Authorize。该 ctx 只携带
// 认证层注入的身份键——authn.AuthClaims 加 contextx 的
// user/username/tenant/trace/requestID/platform 六键（pkg/middleware/gin/authn.go:76-89、
// pkg/contextx/context.go），**不含原始 URL path 与方法名**：中间件在本地变量里
// 算出 object/action 后并未回写 ctx。故本装饰器记的是判定三元组的归一化形态
// （不改框架加键，也不从 object 反推 path——DefaultHTTPObject 是有损投影，
// "/v1/permission-groups" 与 "/v1/permission-groups/{id}" 同归一为
// "permission-groups"，反推等于造假数据）：
//
//	tenantID      ← authn.AuthClaims.TenantID（与 handler 的 tenantOf 同源，
//	                permgroup.go:35-40；认证层是唯一可信租户来源）
//	userID        ← subject（中间件默认 subjectResolver = claims.Subject）
//	permissionID  ← obj，即 DefaultHTTPObject/DefaultGRPCObject 解析出的资源标识
//	requestPath   ← 同上 obj——字段名沿用源 schema，语义是「授权资源标识」而非原始路径
//	requestMethod ← act，即 DefaultHTTPAction（小写 HTTP 方法）/ DefaultGRPCAction
//	result        ← 内层判定原值（allow 与 deny 都记：schema 的 Result bool 即为此设计）
//	effectDetails ← 主体角色快照（拒绝时的第一手线索：持有哪些角色仍未命中任何 p 行）
//
// ## 写放大与 best-effort（务必阅读）
//
//   - 每个通过认证层的请求多一条 INSERT。admin 后台是低 QPS 场景，可接受；
//     高 QPS 部署接本装饰器前应改为批量/异步落库。
//   - 记录是**旁路**：写入报错或 panic 只降级记一条日志（与框架 audit 中间件
//     recordSafely 同纪律，pkg/middleware/gin/audit.go:219-230），绝不改变判定
//     结果——授权是安全边界，日志是观测数据，二者不可互害。
//   - 引擎错误（err != nil）不记：那是 500 路径，不是一次策略评估结论。
//   - 装饰器装在最外层（casbin → reloadable → alias → recording），别名重试
//     发生在内层 ⇒ 一次请求只产生一条日志。
package authz

import (
	"context"
	"strings"

	"github.com/kalandramo/bald/log"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
)

// EvaluationRecorder 是判定结果的落库面——permgroup.Biz.RecordEvaluation 的签名
// 逐字即此（装饰器不 import biz 包：permgroup 的测试文件已 import bootstrap 取仓储
// 聚合，bootstrap 反向 import permgroup 会在测试构建期成环，桥接见
// internal/bootstrap 的 SetEvalRecorder）。
type EvaluationRecorder interface {
	RecordEvaluation(ctx context.Context, tenantID, userID, permissionID,
		requestPath, requestMethod string, allowed bool, details string) error
}

// RecordingAuthorizer 是把授权判定写入策略评估日志的装饰器。
//
// 并发安全：只读字段，构造后不可变，与 Reloadable/ObjectAlias 同形态。
type RecordingAuthorizer struct {
	inner authz.Authorizer
	// recorder 迟到取用：授权器在 InitBridges 构造，permgroup 域 biz 要到域
	// Wire 才存在——传 getter 而非实例，装配期零依赖（getter 本身或产物为 nil
	// 即纯透传，判定语义与接线前逐字一致）。
	recorder func() EvaluationRecorder
}

// NewRecording 用内层授权器与写入面 getter 构造装饰器。
func NewRecording(inner authz.Authorizer, recorder func() EvaluationRecorder) *RecordingAuthorizer {
	return &RecordingAuthorizer{inner: inner, recorder: recorder}
}

// Authorize 实现 authz.Authorizer：委托内层，**原样返回内层的 (allowed, err)**，
// 只在判定成功（无引擎错误）时补记一条评估日志。
func (r *RecordingAuthorizer) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	if r.inner == nil {
		// 无内层授权器：fail-closed（与 Reloadable/ObjectAlias 同语义——授权是
		// 安全边界，缺失时不能放行）。
		return false, nil
	}
	allowed, err := r.inner.Authorize(ctx, subject, object, action)
	if err == nil {
		r.record(ctx, subject, object, action, allowed)
	}
	return allowed, err
}

// record 把判定结果写入评估日志，全程 best-effort：写入面缺失即跳过，
// 报错降级 warn，panic 由 recover 兜住——三条路径都不改变已得出的判定结果。
func (r *RecordingAuthorizer) record(ctx context.Context, subject, object, action string, allowed bool) {
	if r.recorder == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			log.Error(ctx, "policy evaluation record panicked", "panic", rec, "object", object)
		}
	}()
	rec := r.recorder()
	if rec == nil {
		// 装配层尚未注入写入面（如 permgroup 域未 Wire）：降级不记录。
		return
	}
	var tenantID, details string
	if cl := authn.AuthClaimsFromContext(ctx); cl != nil {
		tenantID = cl.TenantID
		if len(cl.Roles) > 0 {
			details = "roles=" + strings.Join(cl.Roles, ",")
		}
	}
	if err := rec.RecordEvaluation(ctx, tenantID, subject, object, object, action, allowed, details); err != nil {
		log.Warn(ctx, "policy evaluation log write failed (authorization unaffected)",
			"error", err, "subject", subject, "object", object, "action", action)
	}
}

// 编译期契约：装饰器是合法 authz.Authorizer。
var _ authz.Authorizer = (*RecordingAuthorizer)(nil)
