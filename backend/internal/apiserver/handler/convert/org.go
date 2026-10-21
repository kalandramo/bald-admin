package convert

// org.go org 域（identity）的 DTO → proto 转换。

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
)

// OrgUnitToPB org 单元 DTO → proto（含 children 递归）。
func OrgUnitToPB(m *orgbiz.OrgUnit) *identityv1.OrgUnit {
	pb := &identityv1.OrgUnit{
		Id: m.ID, Code: m.Code, Name: m.Name,
		Type: parseOrgType(m.Type), Status: parseOrgStatus(m.Status),
		ParentId: m.ParentID, Path: m.Path, SortOrder: m.SortOrder,
		LeaderId: m.LeaderID, LeaderName: m.LeaderName,
		Remark: m.Remark, Description: m.Description,
	}
	if len(m.Children) > 0 {
		pb.Children = make([]*identityv1.OrgUnit, 0, len(m.Children))
		for _, ch := range m.Children {
			pb.Children = append(pb.Children, OrgUnitToPB(ch))
		}
	}
	return pb
}

// PositionToPB 职位 DTO → proto。
func PositionToPB(m *orgbiz.Position) *identityv1.Position {
	pb := &identityv1.Position{
		Id: m.ID, Code: m.Code, Name: m.Name,
		Headcount: m.Headcount, SortOrder: m.SortOrder,
		Status: parsePosStatus(m.Status), Type: parsePosType(m.Type),
		Remark: m.Remark, Description: m.Description,
		JobFamily: m.JobFamily, JobGrade: m.JobGrade,
		Level: m.Level, IsKeyPosition: m.IsKeyPosition,
		OrgUnitId: m.OrgUnitID, OrgUnitName: m.OrgUnitName,
		ReportsToPositionId: m.ReportsToPositionID, ReportsToPositionName: m.ReportsToPositionName,
	}
	if m.StartAt != nil && !m.StartAt.IsZero() {
		pb.StartAt = timestamppb.New(*m.StartAt)
	}
	return pb
}

// parseOrgStatus 字符串状态 → 枚举（未知值回落 UNSPECIFIED）。
func parseOrgStatus(s string) identityv1.OrgUnit_Status {
	if v, ok := identityv1.OrgUnit_Status_value[s]; ok {
		return identityv1.OrgUnit_Status(v)
	}
	return identityv1.OrgUnit_STATUS_UNSPECIFIED
}

// parseOrgType 字符串类型 → 枚举。
func parseOrgType(s string) identityv1.OrgUnit_Type {
	if v, ok := identityv1.OrgUnit_Type_value[s]; ok {
		return identityv1.OrgUnit_Type(v)
	}
	return identityv1.OrgUnit_TYPE_UNSPECIFIED
}

// parsePosStatus 字符串状态 → 枚举。
func parsePosStatus(s string) identityv1.Position_Status {
	if v, ok := identityv1.Position_Status_value[s]; ok {
		return identityv1.Position_Status(v)
	}
	return identityv1.Position_STATUS_UNSPECIFIED
}

// parsePosType 字符串类型 → 枚举。
func parsePosType(s string) identityv1.Position_Type {
	if v, ok := identityv1.Position_Type_value[s]; ok {
		return identityv1.Position_Type(v)
	}
	return identityv1.Position_TYPE_UNSPECIFIED
}
