// Package casbin 是 bald-admin 的授权装配薄壳（引入，P11 起实现晋升 contrib）。
//
// 职责变化（见 docs/devel/zh-CN/架构优化路线.md）：casbin 桥接的**实现**已晋升为
// contrib module（github.com/kalandramo/bald/contrib/authz-casbin，内嵌通用 RBAC 模型 + 纯 Enforce）；
// 本包构造器接收**业务策略数据**（csv 文本）并注入 contrib。传输中立归一化仍由核心
// 拦截器完成。
//
// 策略数据化：csv 文本由 bootstrap 从 DB 装载（p 行 = RolePolicy 表，
// g 行 = User.Roles）——策略单一真源在 DB，
// 改库重启即生效；无策略行时 casbin 默认拒绝（fail-closed）。
package casbin

import (
	contribcasbin "github.com/kalandramo/bald/contrib/authz-casbin"
)

// New 用业务策略 csv + contrib 内嵌的通用 RBAC 模型构造授权器。
func New(policyCSV string) (*contribcasbin.Authorizer, error) {
	return contribcasbin.New(policyCSV)
}
