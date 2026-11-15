// Package app 的 CLI 自定义接线（bald gen app 生成一次，此后归你维护；
// 重生成跳过本文件）。
package app

import "github.com/spf13/cobra"

// customSubcommands 返回本角色额外挂到根命令的业务子命令（与默认 serve
// 并存）。生成物只调用本函数，不覆盖本文件。
func customSubcommands() []*cobra.Command { return nil }
