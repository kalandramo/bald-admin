// alias.go 授权 object 别名装饰器（Wave 1.7 gRPC 补实现配套修复）。
//
// 解决的问题（本会话实测复现）：
//
//	P9 传输中立归一化用 authz.DefaultGRPCObject 把 gRPC FullMethod 的 Service 段
//	压成小写资源名（取最后一段、去 "Service" 后缀、转小写），**不插入任何分隔符**：
//	    OrgUnitService   -> "orgunit"     DictTypeService -> "dicttype"
//	    PositionService  -> "position"    DictEntryService -> "dictentry"
//	而 casbin 策略（internal/bootstrap seedPolicies）的 object 沿用 **HTTP 路径段**
//	风格（DefaultHTTPObject("/v1/org-units") = "org-units"）：
//	    org-units / positions / dict_type / dict_entry / admin_portal / redis-cache-monitor
//	casbin 模型是精确匹配（contrib/authz-casbin/rbac_model.conf: r.obj == p.obj），
//	故 gRPC 面 Enforce("orgunit") 恒 false —— admin 也被 403。
//
// 实测（探针复现，已清理）：
//
//	gRPC ...OrgUnitService/ListOrgUnits  obj="orgunit"  allowed=false
//	HTTP /v1/org-units                   obj="org-units" allowed=true
//
// 为什么不在框架侧修归一化算法：
//   - 驼峰转分隔符治不了**单复数**差异（OrgUnitService -> "org-unit" ≠ "org-units"）；
//   - bald 是零 replace 的外部 module，改框架需发版。
//
// 为什么不用「策略双写」：
//   - 双写会在 RolePolicy 表产生影子行，两处命名需同步维护，真源分裂。
//
// 本方案（应用层装饰器，与 ReloadableAuthorizer 同款手法）：
//   内层明确拒绝后，按别名表用**另一个 object 名**重试一次。
//   重试的是同一 subject/action 下的同一资源的不同书写形态——不放宽任何语义边界。
package authz

import (
	"context"

	"github.com/kalandramo/bald/pkg/authz"
)

// ObjectAliasAuthorizer 在 object 名未命中策略时，按别名表重试一次。
//
// 语义边界（安全相关，勿放宽）：
//   - 别名只在**内层明确返回 (false, nil)** 后触发——「拒绝」才重试，不是「拒绝就放行」；
//   - 重试使用**同一个 subject 与 action**，只换 object 名；
//   - 内层返回 error（引擎故障）时**不重试**，原样透传（避免把故障误判成命名不匹配）；
//   - 别名命中仍需内层放行——装饰器不放行任何内层拒绝的请求，只换一个名字再问一次。
type ObjectAliasAuthorizer struct {
	inner   authz.Authorizer
	aliases map[string]string
}

// NewObjectAlias 用内层授权器与别名表构造装饰器。
// aliases 为 nil/空时装饰器等价于直接委托内层。
func NewObjectAlias(inner authz.Authorizer, aliases map[string]string) *ObjectAliasAuthorizer {
	return &ObjectAliasAuthorizer{inner: inner, aliases: aliases}
}

// Authorize 实现 authz.Authorizer。
func (a *ObjectAliasAuthorizer) Authorize(ctx context.Context, subject, object, action string) (bool, error) {
	if a.inner == nil {
		// 无内层授权器：fail-closed（与 ReloadableAuthorizer 同语义——授权是安全边界，
		// 缺失时不能放行）。
		return false, nil
	}

	allowed, err := a.inner.Authorize(ctx, subject, object, action)
	if err != nil || allowed {
		// 放行，或引擎出错——都不进入别名重试。
		return allowed, err
	}

	// 明确拒绝：若该 object 有别名，用别名再问一次。
	alias, ok := a.aliases[object]
	if !ok || alias == "" || alias == object {
		return false, nil
	}
	return a.inner.Authorize(ctx, subject, alias, action)
}

// 编译期契约：装饰器是合法 authz.Authorizer。
var _ authz.Authorizer = (*ObjectAliasAuthorizer)(nil)
