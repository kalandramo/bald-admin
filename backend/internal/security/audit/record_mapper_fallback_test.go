package audit

// record_mapper_fallback_test.go —— 审计兜底租户的可配置性（2026-09-22）。
//
// 背景：三类审计事件天然无租户（login 失败/permission/data_access），落库时
// 需兜底——否则在租户隔离下查不到。兜底值原为写死常量，现改为可经配置段
// `audit.fallback_tenant` 覆盖（SetFallbackTenant）。本测试锁定三种情形。

import (
	"testing"
	"time"

	"github.com/kalandramo/bald/pkg/audit"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

// TestFallbackTenant_Default 未配置时用 DefaultTenantID。
func TestFallbackTenant_Default(t *testing.T) {
	SetFallbackTenant("") // 空 → 回落默认
	m := NewRecordMapper("")(audit.AuditEvent{
		Time:    time.Now(),
		Subject: "u-attacker", // 用户名线索
		// TenantID 故意留空：模拟 login 失败（用户不存在）
	})
	rec, ok := m.(*authmodel.AuditRecord)
	if !ok {
		t.Fatalf("映射返回值类型=%T, want *authmodel.AuditRecord", m)
	}
	if rec.TenantID != DefaultTenantID {
		t.Fatalf("tenant_id=%q, want %q（默认兜底）", rec.TenantID, DefaultTenantID)
	}
	// 关键：用户名（Subject）与租户是两个独立字段，兜底不影响用户名记录。
	if rec.Subject != "u-attacker" {
		t.Fatalf("subject=%q, want u-attacker（用户名须照常记录）", rec.Subject)
	}
}

// TestFallbackTenant_Configured 配置后覆盖默认值（本波新增能力）。
func TestFallbackTenant_Configured(t *testing.T) {
	t.Cleanup(func() { SetFallbackTenant("") }) // 还原，避免污染其他测试

	SetFallbackTenant("t-custom-fallback")
	m := NewRecordMapper("")(audit.AuditEvent{Time: time.Now()})
	rec := m.(*authmodel.AuditRecord)
	if rec.TenantID != "t-custom-fallback" {
		t.Fatalf("tenant_id=%q, want t-custom-fallback（配置应生效）", rec.TenantID)
	}
}

// TestFallbackTenant_EventTenantWins 事件自带租户时不被兜底覆盖。
//
// 这是不变量：兜底只作用于**空租户**事件，有租户的事件必须保持原值——
// 否则登录成功（u.TenantID 有值）的审计会被错误归到兜底租户。
func TestFallbackTenant_EventTenantWins(t *testing.T) {
	SetFallbackTenant("t-fallback")
	m := NewRecordMapper("")(audit.AuditEvent{
		Time:     time.Now(),
		TenantID: "t-real", // 事件自带租户
	})
	rec := m.(*authmodel.AuditRecord)
	if rec.TenantID != "t-real" {
		t.Fatalf("tenant_id=%q, want t-real（事件租户优先于兜底）", rec.TenantID)
	}
}

// TestFallbackTenant_ExplicitArgWins 工厂显式传参优先于全局设置。
func TestFallbackTenant_ExplicitArgWins(t *testing.T) {
	SetFallbackTenant("t-global")
	m := NewRecordMapper("t-explicit")(audit.AuditEvent{Time: time.Now()})
	rec := m.(*authmodel.AuditRecord)
	if rec.TenantID != "t-explicit" {
		t.Fatalf("tenant_id=%q, want t-explicit（工厂显式传参优先）", rec.TenantID)
	}
}
