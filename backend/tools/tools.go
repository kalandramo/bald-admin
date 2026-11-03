//go:build tools

// 工具依赖注册表：本包只在 build tag `tools` 下参与编译，作用是让
// `go mod tidy` 保留 bald/cli 的 require——task gen:domain / gen:check 跑的是
// `go run github.com/kalandramo/bald/cli/cmd/bald`，该路径不是本仓库的 import，
// tidy 会把 require 删掉（`go mod why` 报 main module does not need package）。
package tools

import (
	_ "github.com/kalandramo/bald/cli/cmd/bald"
)
