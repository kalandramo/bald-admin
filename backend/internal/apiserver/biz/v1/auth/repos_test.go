package auth

// repos_test.go —— Wave 5：本包共享的仓储聚合句柄（见 internal/bootstrap
// 的 Repositories）。测试在 InitBridges 后经 LastRepositories() 赋值。
//
// 可安全共享：InitBridges 幂等（返回同一实例）。

import bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"

var repos *bootstrappkg.Repositories
