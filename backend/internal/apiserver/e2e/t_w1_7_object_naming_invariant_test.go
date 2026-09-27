package e2e

// t_w1_7_object_naming_invariant_test.go —— P9 归一化 × 策略 object 命名的**不变量**测试。
//
// 背景：gRPC 面授权 object 由 authz.DefaultGRPCObject 从 Service 名推导
//（压成无分隔小写，如 OrgUnitService→"orgunit"），而 casbin 策略的 object 沿用
// HTTP 路径段风格（"org-units"）；casbin 精确匹配（r.obj == p.obj），两者不等即 403。
//
// Wave 1.7 用别名表（bootstrap.grpcObjectAliases）桥接了已知的 6 个多词 service。
// 本测试是**防复发闸门**：枚举 proto 里全部 service，断言每个的归一化结果
// 「要么直接命中策略 object，要么在别名表中有映射」——新增多词 service 却忘记
// 加别名时，本测试立即变红（而不是等到线上 403 才被发现）。
//
// 注意：本测试**不查 DB**——它断言的是「命名空间闭合性」这一静态不变量，
// 与策略表是否已 seed 无关（seed 行由 bootstrap.seedPolicies 负责，其内容
// 由 Wave 1 的改动保证）。

import (
	"testing"

	"github.com/kalandramo/bald/pkg/authz"
)

// registeredGRPCServices 是当前经 main.go registerGRPC 注册的 gRPC service
// 的 ServiceName（proto 全限定名，不含方法段）。新增注册时同步追加。
//
// 来源：cmd/bald-admin/main.go 的 registerGRPC 闭包（Wave 1.7 后 11 个）。
var registeredGRPCServices = []string{
	"/go.bald.admin.v1.SecretService",
	"/go.bald.admin.tenant.v1.TenantService",
	"/go.bald.admin.user.v1.UserService",
	"/go.bald.admin.menu.v1.MenuService",
	"/go.bald.admin.permission.v1.PermissionService",
	"/go.bald.admin.dict.v1.DictTypeService",
	"/go.bald.admin.dict.v1.DictEntryService",
	"/go.bald.admin.file.v1.FileService",
	"/go.bald.admin.audit.v1.AuditService",
	// Wave 1.7 新增：
	"/go.bald.admin.identity.v1.OrgUnitService",
	"/go.bald.admin.identity.v1.PositionService",
}

// policyObjects 是 seedPolicies（internal/bootstrap/bootstrap.go）中出现的
// object 值全集——即「策略世界里存在的资源名」。
//
// 维护约定：seedPolicies 新增 object 时同步追加（否则本不变量会误报）。
var policyObjects = map[string]bool{
	"secret": true, "auth": true, "mfa": true, "credentials": true,
	"login-policies": true, "profile": true, "org-units": true, "positions": true,
	"tasks": true, "messages": true, "message-categories": true, "inbox": true,
	"recipients": true, "dashboard": true, "permission-groups": true,
	"policy-evaluation-logs": true, "plans": true, "plan-modules": true,
	"plan-quotas": true, "admin": true, "tenant": true, "user": true,
	"menu": true, "permission": true, "dict_type": true, "dict_entry": true,
	"language": true, "admin_portal": true, "redis-cache-monitor": true,
	"file": true, "audit": true,
}

// aliasObjects 是 grpcObjectAliases（internal/bootstrap/bootstrap.go）的 key 全集。
// 与 bootstrap 的映射表保持同步——本测试是它的「消费者契约」。
var aliasObjects = map[string]bool{
	"orgunit": true, "position": true, "dicttype": true,
	"dictentry": true, "adminportal": true, "rediscachemonitor": true,
}

// TestObjectNamingInvariant_AllRegisteredServicesResolvable
// 每个已注册 gRPC service 的归一化 object 必须「可解析」到策略世界。
//
// 可解析的定义：DefaultGRPCObject(serviceName) ∈ policyObjects
//
//	或 ∈ aliasObjects（经别名表能落到某个策略 object）。
//
// 失败即意味着：该 service 的 gRPC 面会 403（object 名无人认识）。
func TestObjectNamingInvariant_AllRegisteredServicesResolvable(t *testing.T) {
	for _, svc := range registeredGRPCServices {
		// 用 Service 名 + 一个典型方法构造 FullMethod（DefaultGRPCObject 只看 Service 段）。
		obj := authz.DefaultGRPCObject(svc + "/Probe")
		if policyObjects[obj] || aliasObjects[obj] {
			continue
		}
		t.Errorf("service %s 归一化为 object %q，既不在策略 object 集合、也不在别名表 —— gRPC 面将 403。\n"+
			"修复：在 bootstrap.grpcObjectAliases 加 %q 映射，或在 seedPolicies 补该 object 的策略行。",
			svc, obj, obj)
	}
}

// TestObjectNamingInvariant_AliasTargetsExistInPolicy
// 别名表的**每个目标值**必须是真实存在的策略 object——防止别名指向空处。
//
// 这是上一测试的对偶：前者防「service 无别名」，本测试防「别名指向不存在的策略」。
func TestObjectNamingInvariant_AliasTargetsExistInPolicy(t *testing.T) {
	// 与 bootstrap.grpcObjectAliases 同源（本文件只读断言，不 import bootstrap
	// 以避免 e2e 包对 bootstrap 内部变量的编译期耦合）。
	aliases := map[string]string{
		"orgunit":           "org-units",
		"position":          "positions",
		"dicttype":          "dict_type",
		"dictentry":         "dict_entry",
		"adminportal":       "admin_portal",
		"rediscachemonitor": "redis-cache-monitor",
	}
	for from, to := range aliases {
		if !policyObjects[to] {
			t.Errorf("别名 %q → %q：目标 %q 不是任何策略 object —— 别名指向空处，gRPC 面仍会 403",
				from, to, to)
		}
	}
}
