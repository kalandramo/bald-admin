package convert

// org_test.go 锁定 org 域转换函数的输出（重构期间的行为等价锚点）。
//
// 这些断言是 Wave 1 迁移的**回归基线**：迁移前后必须给出同样的 pb 结构。

import (
	"testing"
	"time"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
)

// TestOrgUnitToPB_FieldMapping 逐字段核对 DTO → pb 的映射。
func TestOrgUnitToPB_FieldMapping(t *testing.T) {
	in := &orgbiz.OrgUnit{
		ID: "t1:FIN", Name: "财务部", Code: "FIN", Type: "TYPE_DEPARTMENT",
		ParentID: "HQ", Path: "/1/2", Status: "STATUS_ON", SortOrder: 3,
		LeaderID: "u-1", LeaderName: "张三",
		Remark: "备注", Description: "描述",
	}
	pb := OrgUnitToPB(in)

	if pb.GetId() != "t1:FIN" || pb.GetCode() != "FIN" || pb.GetName() != "财务部" {
		t.Fatalf("基础字段映射错: id=%q code=%q name=%q", pb.GetId(), pb.GetCode(), pb.GetName())
	}
	if pb.GetType() != identityv1.OrgUnit_TYPE_DEPARTMENT {
		t.Errorf("type=%v, want TYPE_DEPARTMENT", pb.GetType())
	}
	if pb.GetStatus() != identityv1.OrgUnit_STATUS_ON {
		t.Errorf("status=%v, want STATUS_ON", pb.GetStatus())
	}
	if pb.GetParentId() != "HQ" || pb.GetPath() != "/1/2" || pb.GetSortOrder() != 3 {
		t.Errorf("parent/path/sort 映射错: %q %q %d", pb.GetParentId(), pb.GetPath(), pb.GetSortOrder())
	}
	if pb.GetLeaderId() != "u-1" || pb.GetLeaderName() != "张三" {
		t.Errorf("leader 映射错: %q %q", pb.GetLeaderId(), pb.GetLeaderName())
	}
	if pb.GetRemark() != "备注" || pb.GetDescription() != "描述" {
		t.Errorf("remark/description 映射错")
	}
	if len(pb.GetChildren()) != 0 {
		t.Errorf("无 children 时不应产生空切片，实得 %d", len(pb.GetChildren()))
	}
}

// TestOrgUnitToPB_ChildrenRecursive children 递归回填。
func TestOrgUnitToPB_ChildrenRecursive(t *testing.T) {
	leaf := &orgbiz.OrgUnit{ID: "t1:BE", Name: "后端组", Code: "BE"}
	mid := &orgbiz.OrgUnit{ID: "t1:DEV", Name: "研发部", Code: "DEV", Children: []*orgbiz.OrgUnit{leaf}}
	root := &orgbiz.OrgUnit{ID: "t1:HQ", Name: "总部", Code: "HQ", Children: []*orgbiz.OrgUnit{mid}}

	pb := OrgUnitToPB(root)
	if len(pb.GetChildren()) != 1 {
		t.Fatalf("根应有 1 个子节点，实得 %d", len(pb.GetChildren()))
	}
	if pb.GetChildren()[0].GetCode() != "DEV" {
		t.Fatalf("一级子应 DEV，实得 %q", pb.GetChildren()[0].GetCode())
	}
	grand := pb.GetChildren()[0].GetChildren()
	if len(grand) != 1 || grand[0].GetCode() != "BE" {
		t.Fatalf("二级孙应 BE，实得 %+v", grand)
	}
}

// TestOrgUnitToPB_UnknownEnumFallback 未知枚举回落 UNSPECIFIED（两侧原有语义）。
func TestOrgUnitToPB_UnknownEnumFallback(t *testing.T) {
	pb := OrgUnitToPB(&orgbiz.OrgUnit{Type: "NOT_A_TYPE", Status: "NOT_A_STATUS"})
	if pb.GetType() != identityv1.OrgUnit_TYPE_UNSPECIFIED {
		t.Errorf("未知 type 应回落 UNSPECIFIED，实得 %v", pb.GetType())
	}
	if pb.GetStatus() != identityv1.OrgUnit_STATUS_UNSPECIFIED {
		t.Errorf("未知 status 应回落 UNSPECIFIED，实得 %v", pb.GetStatus())
	}
}

// TestPositionToPB_FieldMapping 职位逐字段映射。
func TestPositionToPB_FieldMapping(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := &orgbiz.Position{
		ID: "t1:pos:CFO", Code: "CFO", Name: "财务总监",
		Headcount: 2, SortOrder: 1, Status: "STATUS_ON", Type: "TYPE_LEADER",
		Remark: "r", Description: "d", JobFamily: "FIN", JobGrade: "P8",
		Level: 8, IsKeyPosition: true,
		OrgUnitID: "FIN", OrgUnitName: "财务部",
		ReportsToPositionID: "CEO", ReportsToPositionName: "总经理",
		StartAt: &start,
	}
	pb := PositionToPB(in)

	if pb.GetCode() != "CFO" || pb.GetName() != "财务总监" || pb.GetHeadcount() != 2 {
		t.Fatalf("基础字段映射错")
	}
	if pb.GetStatus() != identityv1.Position_STATUS_ON || pb.GetType() != identityv1.Position_TYPE_LEADER {
		t.Errorf("枚举映射错: %v %v", pb.GetStatus(), pb.GetType())
	}
	if pb.GetOrgUnitId() != "FIN" || pb.GetOrgUnitName() != "财务部" {
		t.Errorf("org 关联映射错")
	}
	if pb.GetReportsToPositionId() != "CEO" || pb.GetReportsToPositionName() != "总经理" {
		t.Errorf("reports_to 映射错")
	}
	if !pb.GetIsKeyPosition() || pb.GetLevel() != 8 || pb.GetJobGrade() != "P8" {
		t.Errorf("职级字段映射错")
	}
	if pb.GetStartAt() == nil || !pb.GetStartAt().AsTime().Equal(start) {
		t.Errorf("start_at 映射错: %v", pb.GetStartAt())
	}
}

// TestPositionToPB_NilStartAt 零值 StartAt 不产生 Timestamp。
func TestPositionToPB_NilStartAt(t *testing.T) {
	if pb := PositionToPB(&orgbiz.Position{}); pb.GetStartAt() != nil {
		t.Errorf("nil StartAt 应不产出 Timestamp，实得 %v", pb.GetStartAt())
	}
	zero := time.Time{}
	if pb := PositionToPB(&orgbiz.Position{StartAt: &zero}); pb.GetStartAt() != nil {
		t.Errorf("零值 StartAt 应不产出 Timestamp（IsZero 分支），实得 %v", pb.GetStartAt())
	}
}
