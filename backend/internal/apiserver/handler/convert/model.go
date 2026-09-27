package convert

// model.go 基于 model 实体的域转换（tenant/user/menu/dict/language/permission/audit/file）。
//
// 逐字迁自 handler/gin/*.go 与 handler/grpc/*.go（两侧 diff 归一化后同构，
// 2026-09-27 逐函数实测；例外见各函数注释）。

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/kalandramo/bald-admin/api/gen/go/audit/v1"
	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	filev1 "github.com/kalandramo/bald-admin/api/gen/go/file/v1"
	menuv1 "github.com/kalandramo/bald-admin/api/gen/go/menu/v1"
	permissionv1 "github.com/kalandramo/bald-admin/api/gen/go/permission/v1"
	tenantv1 "github.com/kalandramo/bald-admin/api/gen/go/tenant/v1"
	userv1 "github.com/kalandramo/bald-admin/api/gen/go/user/v1"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

// TenantToPB 租户 model → proto。
func TenantToPB(t *authmodel.Tenant) *tenantv1.Tenant {
	pb := &tenantv1.Tenant{
		Id:        t.ID,
		Name:      t.Name,
		Remark:    t.Remark,
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
	if v, ok := tenantv1.Tenant_Status_value[t.Status]; ok {
		pb.Status = tenantv1.Tenant_Status(v)
	}
	return pb
}

// UserToPB 用户 model → proto（密码哈希不外泄）。
func UserToPB(u *authmodel.User) *userv1.User {
	return &userv1.User{
		Id:        u.ID,
		Username:  u.Username,
		Roles:     u.RolesList(),
		CreatedAt: timestamppb.New(u.CreatedAt),
		UpdatedAt: timestamppb.New(u.UpdatedAt),
	}
}

// MenuToPB 菜单 model → proto（平铺，含枚举解析）。
func MenuToPB(m *authmodel.Menu) *menuv1.Menu {
	pb := &menuv1.Menu{
		Id:        m.ID,
		ParentId:  m.ParentID,
		Name:      m.Name,
		Path:      m.Path,
		Component: m.Component,
		Title:     m.Title,
		Icon:      m.Icon,
		Order:     m.Order,
		Remark:    m.Remark,
		CreatedAt: timestamppb.New(m.CreatedAt),
		UpdatedAt: timestamppb.New(m.UpdatedAt),
	}
	if v, ok := menuv1.Menu_Type_value[m.Type]; ok {
		pb.Type = menuv1.Menu_Type(v)
	}
	if v, ok := menuv1.Menu_Status_value[m.Status]; ok {
		pb.Status = menuv1.Menu_Status(v)
	}
	return pb
}

// MenuToPBTree 菜单 model → proto（含 children 递归）。
func MenuToPBTree(m *authmodel.Menu) *menuv1.Menu {
	pb := MenuToPB(m)
	for _, ch := range m.Children {
		pb.Children = append(pb.Children, MenuToPBTree(ch))
	}
	return pb
}

// DictTypeToPB 字典类型 model → proto。
func DictTypeToPB(t *authmodel.DictType) *dictv1.DictType {
	return &dictv1.DictType{
		Id:        t.ID,
		TypeName:  t.TypeName,
		SortOrder: t.SortOrder,
		Enabled:   t.Enabled,
		Remark:    t.Remark,
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
}

// DictEntryToPB 字典项 model → proto。
func DictEntryToPB(e *authmodel.DictEntry) *dictv1.DictEntry {
	return &dictv1.DictEntry{
		Id:        e.ID,
		TypeCode:  e.TypeCode,
		Value:     e.Value,
		Label:     e.Label,
		Numeric:   e.Numeric,
		SortOrder: e.SortOrder,
		Enabled:   e.Enabled,
		Remark:    e.Remark,
		CreatedAt: timestamppb.New(e.CreatedAt),
		UpdatedAt: timestamppb.New(e.UpdatedAt),
	}
}

// LanguageToPB 语言 model → proto（零值时间不产出 Timestamp）。
//
// 注：**仅 gin 侧曾有该函数**——gRPC 侧未实现 LanguageService（`handler/grpc/`
// 无 language.go，`main.go` 的 registerGRPC 也未注册），故无 grpc 副本可合并。
func LanguageToPB(m *authmodel.Language) *dictv1.Language {
	pb := &dictv1.Language{
		Id:           m.ID,
		LanguageName: m.LanguageName,
		NativeName:   m.NativeName,
		IsDefault:    m.IsDefault,
		IsEnabled:    m.IsEnabled,
		SortOrder:    m.SortOrder,
	}
	if !m.CreatedAt.IsZero() {
		pb.CreatedAt = timestamppb.New(m.CreatedAt)
	}
	if !m.UpdatedAt.IsZero() {
		pb.UpdatedAt = timestamppb.New(m.UpdatedAt)
	}
	return pb
}

// PermissionToPB 权限 model → proto（MenuIDs CSV 拆为 repeated）。
//
// 迁移说明：gin 侧原调 `splitCSVStrings`、grpc 侧原调 `splitPermissionCSV`——
// 两函数**同算法不同名**（2026-09-27 diff 实测），此处统一为 splitCSV，
// 是本次迁移消除的第一处实质重复。
func PermissionToPB(p *authmodel.Permission) *permissionv1.Permission {
	pb := &permissionv1.Permission{
		Id:        p.ID,
		Name:      p.Name,
		Remark:    p.Remark,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	for _, m := range splitCSV(p.MenuIDs) {
		pb.MenuIds = append(pb.MenuIds, m)
	}
	return pb
}

// RolePolicyToPB 角色策略 model → proto。
func RolePolicyToPB(p *authmodel.RolePolicy) *permissionv1.RolePolicy {
	return &permissionv1.RolePolicy{
		Id:     p.ID,
		Role:   p.Role,
		Object: p.Object,
		Action: p.Action,
	}
}

// AuditRecordToPB 审计记录 model → proto（自增 ID 字符串化；纳秒时间 → Timestamp）。
func AuditRecordToPB(m *authmodel.AuditRecord) *auditv1.AuditRecord {
	return &auditv1.AuditRecord{
		Id:        strconv.FormatUint(uint64(m.ID), 10),
		TenantId:  m.TenantID,
		Category:  m.Category,
		Subject:   m.Subject,
		Object:    m.Object,
		Action:    m.Action,
		Result:    m.Result,
		Error:     m.Error,
		IpAddress: m.IPAddress,
		UserAgent: m.UserAgent,
		RequestId: m.RequestID,
		TraceId:   m.TraceID,
		// Wave 5.1：五类差异字段（各分类按需非空）。
		HttpMethod:   m.HTTPMethod,
		Path:         m.Path,
		StatusCode:   m.StatusCode,
		LatencyMs:    m.LatencyMs,
		TableName:    m.TableName,
		DataSource:   m.DataSource,
		DbUser:       m.DBUser,
		SqlText:      m.SQLText,
		AffectedRows: m.AffectedRows,
		TargetType:   m.TargetType,
		TargetId:     m.TargetID,
		OldValue:     m.OldValue,
		NewValue:     m.NewValue,
		SessionId:    m.SessionID,
		MfaStatus:    m.MFAStatus,
		Time:         timestamppb.New(time.Unix(0, m.Time)),
	}
}

// FileToPB 文件元数据 model → proto。
func FileToPB(m *authmodel.File) *filev1.File {
	return &filev1.File{
		Id:            m.ID,
		Provider:      m.Provider,
		BucketName:    m.BucketName,
		SaveFileName:  m.SaveFileName,
		FileDirectory: m.FileDirectory,
		FileName:      m.FileName,
		Extension:     m.Extension,
		ContentHash:   m.ContentHash,
		Size:          uint32(m.Size),
		LinkUrl:       m.LinkUrl,
		MimeType:      m.MimeType,
		CreatedBy:     m.CreatedBy,
		CreatedAt:     timestamppb.New(m.CreatedAt),
		UpdatedAt:     timestamppb.New(m.UpdatedAt),
	}
}

// splitCSV 逗号分隔串 → 切片（空串返回 nil）。
// 收敛自 gin 的 splitCSVStrings 与 grpc 的 splitPermissionCSV（同算法）。
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if seg := s[start:i]; seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	return out
}
