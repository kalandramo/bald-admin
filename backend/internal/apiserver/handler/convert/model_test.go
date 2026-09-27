package convert

// model_test.go 锁定 splitCSV 的行为（permission 域 MenuIDs 解析的唯一实现）。
//
// 该函数由 gin 的 splitCSVStrings 与 grpc 的 splitPermissionCSV 合并而来，
// 三者实现逐字同构；本测试锁定合并后的等价语义，防止后续改动漂移。

import (
	"testing"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"空串返回 nil", "", nil},
		{"单个值", "1", []string{"1"}},
		{"常规多值", "1,2,3", []string{"1", "2", "3"}},
		{"中间空段被丢弃", "1,,3", []string{"1", "3"}},
		{"末尾逗号", "1,2,", []string{"1", "2"}},
		{"开头逗号", ",1", []string{"1"}},
		{"全逗号返回 nil", ",,,", nil},
		{"非纯数字段", "menu-a,menu-b", []string{"menu-a", "menu-b"}},
		{"不做 trim（锁定既有语义）", " 1 , 2 ", []string{" 1 ", " 2 "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitCSV(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitCSV(%q) = %#v（len %d），want %#v（len %d）",
					tc.in, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitCSV(%q)[%d] = %q，want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestPermissionToPB_MenuIDsCSV 端到端核对 MenuIDs CSV → repeated 字段。
// 空段丢弃是 PermissionToPB 链路上的真实行为（loadPolicy 装载前会经过它）。
func TestPermissionToPB_MenuIDsCSV(t *testing.T) {
	pb := PermissionToPB(&authmodel.Permission{
		ID: "perm-1", Name: "菜单权限", MenuIDs: "menu-a,menu-b,,menu-c",
	})

	if got := pb.GetId(); got != "perm-1" {
		t.Fatalf("Id = %q，want %q", got, "perm-1")
	}
	want := []string{"menu-a", "menu-b", "menu-c"}
	if len(pb.GetMenuIds()) != len(want) {
		t.Fatalf("MenuIds = %#v，want %#v", pb.GetMenuIds(), want)
	}
	for i, w := range want {
		if pb.GetMenuIds()[i] != w {
			t.Fatalf("MenuIds[%d] = %q，want %q", i, pb.GetMenuIds()[i], w)
		}
	}
}

// TestPermissionToPB_EmptyMenuIDs 空 MenuIDs 应产出 nil repeated（不发空串元素）。
func TestPermissionToPB_EmptyMenuIDs(t *testing.T) {
	pb := PermissionToPB(&authmodel.Permission{ID: "perm-2"})
	if got := pb.GetMenuIds(); len(got) != 0 {
		t.Fatalf("空 MenuIDs 应产出 0 个元素，实得 %#v", got)
	}
}
