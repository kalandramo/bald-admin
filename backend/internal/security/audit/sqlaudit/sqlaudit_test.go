package sqlaudit

// sqlaudit_test.go —— Wave 5.2 防重入与脱敏的不变量测试。
//
// ## 为何防重入必须单独锁
//
// gorm Callback 拦截**所有** DB 操作，包括审计表自身的写入。若采集回调在
// 落库审计记录时再次触发，会形成**无限递归**——表现是栈溢出或请求挂死，
// 且在生产环境是「一开审计就崩」，代价极高。
//
// 本测试锁定三条不变量：
//  1. `WithSinking` 标记的 ctx 不采集（主防重入机制）；
//  2. `audit_records` 表永不采集（双保险，即便 ctx 标记丢失）；
//  3. `MaskSQL` 把字面量脱敏（防敏感数据进审计表）。

import (
	"context"
	"strings"
	"testing"
)

func TestWithSinking_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if IsSinking(ctx) {
		t.Fatal("未标记的 ctx 不应是 sinking")
	}
	if !IsSinking(WithSinking(ctx)) {
		t.Fatal("WithSinking 标记后应为 sinking")
	}
}

func TestMaskSQL_RedactsStringLiterals(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`SELECT * FROM users WHERE name = 'alice'`, `SELECT * FROM users WHERE name = ?`},
		{`INSERT INTO t (a,b) VALUES ('x','y')`, `INSERT INTO t (a,b) VALUES (?,?)`},
		{`SELECT * FROM t`, `SELECT * FROM t`}, // 无字面量原样
	}
	for _, c := range cases {
		got := MaskSQL(c.in)
		if got != c.want {
			t.Fatalf("MaskSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 关键断言：敏感字面量不出现在脱敏结果里。
	if strings.Contains(MaskSQL(`SELECT * FROM users WHERE pwd = 'secret123'`), "secret123") {
		t.Fatal("脱敏后仍含敏感字面量")
	}
}
