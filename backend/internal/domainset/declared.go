package domainset

import "github.com/kalandramo/bald/pkg/appkit"

// Declared 返回 AppSpec.domains 声明的域清单（生成物 domains.gen.go）。
// 未纳入声明的纯 gin 域没有 proto service（gen domain 的 Gate1 要求
// domains.service 与 proto 匹配），仍由装配层手写。
func Declared() []appkit.Option { return domainOptions }
