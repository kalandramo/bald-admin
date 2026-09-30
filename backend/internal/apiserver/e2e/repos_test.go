package e2e

// repos_test.go —— Wave 5：本包共享的仓储聚合句柄。
//
// 背景：Wave 5 把 internal/bootstrap 的 26 个包级 store 变量收进 Repositories
// 聚合，InitBridges 改签名返回 *Repositories。本包内 20+ 个测试 helper 都要
// 「先 InitBridges、再用仓储造数据/装配 biz」，故在包级声明一个 repos 变量，
// 由各 helper 在 InitBridges 后赋值（repos = r）。
//
// 为什么可安全共享：InitBridges 幂等（内部 guard 命中时返回**同一** lastRepos），
// 故无论哪个 helper 先执行、执行几次，repos 始终指向同一套仓储（同一 gorm 连接）。
// 包内测试均为串行（无 t.Parallel），无数据竞争。
//
// 注意：本文件只作声明与文档，不含测试逻辑。

import bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"

// repos 是本包测试共享的仓储聚合。由各 helper 在 InitBridges 之后赋值。
var repos *bootstrappkg.Repositories
