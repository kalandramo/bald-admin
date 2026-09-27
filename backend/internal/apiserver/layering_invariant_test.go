package apiserver_test

// 本文件是 Wave 3「层间解耦」的**机械化不变量测试**（计划 §Wave 3 任务 3.4）。
//
// ## 为什么需要它
//
// bald 项目的《待处理事项》反复出现同一类教训：**同一不变量的多个违反点只修
// 一个**（#3b / #3d / #18）。Wave 3 把 20 个 biz 包对 internal/bootstrap 的
// 依赖（135 处 bootstrappkg.XxxStore）清成零——但这类「结构性约束」如果没有
// 测试固化，下一次新增 biz（或复制旧包作模板）时极易悄悄把依赖加回来，而
// 那正是 G2「控制面单向依赖数据面」被破坏的起点。
//
// ## 断言什么
//
// data 面（internal/apiserver/biz/）下的**非测试** .go 文件，不得 import
// internal/bootstrap。业务依赖走构造期注入（Wave 3），不再请求期回读装配根。
//
// 反例：往任意 biz 包加 `import bootstrappkg "...internal/bootstrap"` 即
// 红——这正是我们想要的「响亮失败」。

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInvariant_BizDoesNotImportBootstrap 锁定 G2 分层边界：
// biz 层不得依赖装配根 internal/bootstrap（Wave 3 已清零，此测试防回退）。
//
// 采用与 platform_superadmin_e2e_test.go 同款的**目录级扫描**而非写死文件
// 清单——后者会因文件搬迁/拆分而假红。扫描 biz 子树全部非测试 .go。
func TestInvariant_BizDoesNotImportBootstrap(t *testing.T) {
	// 相对本测试所在目录（internal/apiserver）定位 biz 子树。
	const bizDir = "biz"
	const forbidden = "bald-admin/internal/bootstrap"

	var violators []string
	err := filepath.WalkDir(bizDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// 测试文件不在约束内：e2e/单测可直接调 InitBridges 搭真实依赖
		//（它们是「消费者」而非「生产者」）。
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), forbidden) {
			violators = append(violators, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 %s: %v", bizDir, err)
	}

	if len(violators) > 0 {
		t.Fatalf("G2 分层边界被破坏：以下 biz 非测试文件 import 了 %s：\n  %s\n"+
			"（业务依赖应经构造期注入——Wave 3 已清零；新增域请勿回读装配根）",
			forbidden, strings.Join(violators, "\n  "))
	}
}
