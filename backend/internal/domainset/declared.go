package domainset

import "github.com/kalandramo/bald/pkg/appkit"

// Declared 返回 AppSpec.domains 声明的域清单（生成物 domains.gen.go）。
// 无 proto service 的域以 httpOnly 声明；authd 是唯一未纳入清单的域
// （需对齐 proto service），仍由装配层手写。
func Declared() []appkit.Option { return domainOptions }
