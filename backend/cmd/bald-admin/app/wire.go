//go:build wireinject
// +build wireinject

package app

import (
	"github.com/google/wire"

	"github.com/kalandramo/bald-admin/internal/apiserver"
)

// InitializeBiz 由 wire 生成实现：显式拼装 cache + 各 biz，依赖图编译期校验。
// T10 起返回 *apiserver.BizSet（聚合结构收敛到 apiserver 包，见其 bizset.go）。
//
// Wave 5：仓储经参数传入（来自 InitBridges 的返回值），不再读 bootstrap 包级变量。
func InitializeBiz(repos *bootstrap.Repositories) (*apiserver.BizSet, error) {
	panic("wire not generated") // 生成实现见 wire_gen.go
}
