package validation

import (
	"context"
	"testing"

	"github.com/kalandramo/bald/berrors"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
)

// TestValidate_CreateOrgUnit_Legal 合法入参应通过（不返回错误）。
func TestValidate_CreateOrgUnit_Legal(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = v.Validate(context.Background(), &identityv1.CreateOrgUnitRequest{
		Code: "FIN-LEADER-001", Name: "财务部",
	})
	if err != nil {
		t.Fatalf("合法入参应通过，got %v", err)
	}
}

// TestValidate_CreateOrgUnit_EmptyCode 空 code 应被拒（注解 min_len:1）。
func TestValidate_CreateOrgUnit_EmptyCode(t *testing.T) {
	v, _ := New()
	err := v.Validate(context.Background(), &identityv1.CreateOrgUnitRequest{
		Code: "", Name: "财务部",
	})
	if err == nil {
		t.Fatal("空 code 应被拒（min_len:1）")
	}
	t.Logf("错误: %v", err)
}

// TestValidate_CreateOrgUnit_EmptyName 空 name 应被拒。
func TestValidate_CreateOrgUnit_EmptyName(t *testing.T) {
	v, _ := New()
	err := v.Validate(context.Background(), &identityv1.CreateOrgUnitRequest{
		Code: "X", Name: "",
	})
	if err == nil {
		t.Fatal("空 name 应被拒（min_len:1）")
	}
}

// TestValidate_CreateOrgUnit_CodeTooLong 超长 code 应被拒（max_len:64）。
func TestValidate_CreateOrgUnit_CodeTooLong(t *testing.T) {
	v, _ := New()
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	err := v.Validate(context.Background(), &identityv1.CreateOrgUnitRequest{
		Code: string(long), Name: "x",
	})
	if err == nil {
		t.Fatal("65 字符 code 应被拒（max_len:64）")
	}
}

// TestValidate_UpdateOrgUnit_EmptyNameAllowed 更新语义：空 name = 不改，**必须放行**。
//
// 这是 Create/Update 的关键差异——若误给 UpdateOrgUnitRequest.name 加 min_len，
// 「只改 parent_id 不传 name」这种正常操作会被误判为非法。
func TestValidate_UpdateOrgUnit_EmptyNameAllowed(t *testing.T) {
	v, _ := New()
	err := v.Validate(context.Background(), &identityv1.UpdateOrgUnitRequest{
		Code: "FIN", Name: "", ParentId: "ROOT",
	})
	if err != nil {
		t.Fatalf("Update 空 name（=不改）应放行，got %v", err)
	}
}

// TestValidate_UpdateOrgUnit_NameTooLong Update 的 max_len 仍应生效。
func TestValidate_UpdateOrgUnit_NameTooLong(t *testing.T) {
	v, _ := New()
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	err := v.Validate(context.Background(), &identityv1.UpdateOrgUnitRequest{
		Code: "FIN", Name: string(long),
	})
	if err == nil {
		t.Fatal("129 字符 name 应被拒（max_len:128）")
	}
}

// TestValidate_CreatePosition_Legal 职位合法入参通过。
func TestValidate_CreatePosition_Legal(t *testing.T) {
	v, _ := New()
	if err := v.Validate(context.Background(), &identityv1.CreatePositionRequest{
		Code: "P-001", Name: "财务总监",
	}); err != nil {
		t.Fatalf("合法职位入参应通过，got %v", err)
	}
}

// TestValidate_CreatePosition_EmptyCode 空 code 被拒。
func TestValidate_CreatePosition_EmptyCode(t *testing.T) {
	v, _ := New()
	if err := v.Validate(context.Background(), &identityv1.CreatePositionRequest{
		Code: "", Name: "财务总监",
	}); err == nil {
		t.Fatal("空 code 应被拒")
	}
}

// TestValidate_UpdatePosition_EmptyNameAllowed 同 Update 语义。
func TestValidate_UpdatePosition_EmptyNameAllowed(t *testing.T) {
	v, _ := New()
	if err := v.Validate(context.Background(), &identityv1.UpdatePositionRequest{
		Code: "P-001", Name: "",
	}); err != nil {
		t.Fatalf("Update 空 name 应放行，got %v", err)
	}
}

// TestValidate_NoAnnotationsMessage 无注解的消息（含零值）应通过。
//
// 渐进推进的安全前提：只给 4 个专用输入消息加了注解，其余消息不受影响。
// GetOrgUnitRequest 未加注解——其零值（空 code）应放行。
func TestValidate_NoAnnotationsMessage(t *testing.T) {
	v, _ := New()
	if err := v.Validate(context.Background(), &identityv1.GetOrgUnitRequest{}); err != nil {
		t.Fatalf("无注解消息零值应通过，got %v", err)
	}
}

// TestValidate_ErrorIsBerrors 校验失败应映射为 berrors（400 语义），非裸 error。
func TestValidate_ErrorIsBerrors(t *testing.T) {
	v, _ := New()
	err := v.Validate(context.Background(), &identityv1.CreateOrgUnitRequest{})
	if err == nil {
		t.Fatal("应报错")
	}
	// 断言是 *berrors.Error 且能取到 reason（供 writeBizErr/ErrorInterceptor 收口）。
	be, ok := berrors.FromError(err)
	if !ok {
		t.Fatalf("错误应为 *berrors.Error（供统一错误出口识别），got %T: %v", err, err)
	}
	if be.Reason == "" {
		t.Errorf("berrors reason 不应为空")
	}
	if be.Code != berrors.CodeInvalidArgument {
		t.Errorf("code=%d, want CodeInvalidArgument(%d)", be.Code, berrors.CodeInvalidArgument)
	}
	t.Logf("berrors: code=%d reason=%q message=%q", be.Code, be.Reason, be.Message)
}
