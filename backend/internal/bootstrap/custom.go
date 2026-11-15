// Package bootstrap 的自定义接线（bald gen app 生成一次，此后归你维护；
// 重生成跳过本文件）。
//
// 生成物 assemble.gen.go 只声明 AppSpec 能表达的东西；第三方 Provider、
// Registrar、拦截器链、探针数据源、Opaque 预算等生成器不认识的接线全部
// 收敛在本文件——装配的声明面仍然只有 Options() 一个入口。
package bootstrap

import (
	"github.com/kalandramo/bald-admin/cmd/bald-admin/app/options"
	"github.com/kalandramo/bald/pkg/appkit"
)

// customOptions 返回应用自定义接线，生成物在 Options() 末尾展开它：追加
// 在末尾 = 同名单值字段末者胜，因此可以覆盖生成的面/健康检查/拦截器默认值。
//
// 本应用的全部业务接线（第三方 Provider、域依赖 Setup、拦截器链、审计协调、
// 生命周期、biz 白名单等）住在 appOptions（assembly.go）；生成物只负责
// std.Standard / ConfigFile / StrictContract / 域清单 / 计划缓存这些声明化的
// 底座，二者合流即生产装配的单一入口 Options()。
func customOptions(_ string) []appkit.Option {
	return appOptionsCore(options.NewServerOptions())
}

// variantOpts 是构建期变体的应用级接线表（键 = AppSpec.variants.name）。
// 变体实现随 //go:build 文件编入时，在带 tag 的文件里登记：
//
//	func init() { variantOpts["grpcgw"] = []appkit.Option{...} }
//
// 生成物 variant_<name>.gen.go 的 init 经 customVariantOpts 取出——变体
// 声明/实现对账仍由生成物负责，业务只在命中时提供接线。
var variantOpts = map[string][]appkit.Option{}

// customVariantOpts 返回变体 name 的自定义接线（未登记返回 nil）。
func customVariantOpts(name string) []appkit.Option { return variantOpts[name] }
