package grpc

// org.go 组织架构域 gRPC service（Wave 1.7 补齐）。
//
// 实现生成的 identityv1.OrgUnitServiceServer（7 rpc）+ PositionServiceServer（7 rpc），
// 经 main.go 的 registerGRPC 注册；gRPC 与 REST 共用同一 org biz 与 casbin 策略
// （P9 归一化：FullMethod → "orgunit"/"position" → 经别名层落到策略 "org-units"/"positions"）。
//
// 与 gin 侧（handler/gin/org.go）的关系：
//   - 同一 biz 路径（biz/v1/org），同一租户语义（claims.TenantID）；
//   - 转换函数在本包**独立实现**（不跨包共享），遵循 menu/tenant 的
//     「包级不共享以避免 gin/grpc 耦合」惯例；
//   - 枚举以**完整枚举名**存储（如 "TYPE_COMPANY"），与 gin 侧一致
//     （gin 用 req.GetType().String()，parseOrgType 经 _value map 反查）。
//
// 租户来源：org biz 的签名要求**显式** tenantID（用于构造 `"<tenant>:<code>"` 主键），
// 与 menu/tenant 等「靠 store 自动注入」的域不同——故从 claims 取值后传入
// （先例：handler/grpc/secret.go:36 取 claims，gin 侧 handler/gin/org.go:54-59 取 tid）。

import (
	"context"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	"github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// tenantIDFromCtx 从认证 claims 取租户 ID（缺失返回空串）。
// org biz 用它构造主键，故与 gin 侧 tid(c) 同语义。
func tenantIDFromCtx(ctx context.Context) string {
	if claims := authn.AuthClaimsFromContext(ctx); claims != nil {
		return claims.TenantID
	}
	return ""
}

// ---- org_unit ----

// orgUnitService 实现生成的 identityv1.OrgUnitServiceServer。
type orgUnitService struct {
	identityv1.UnimplementedOrgUnitServiceServer
	biz *orgbiz.Biz
}

// NewOrgServer 构造 OrgUnitServiceServer 实现（biz 由 wire 装配注入）。
func NewOrgServer(biz *orgbiz.Biz) identityv1.OrgUnitServiceServer {
	return &orgUnitService{biz: biz}
}

func (s *orgUnitService) ListOrgUnits(ctx context.Context, req *identityv1.ListOrgUnitsRequest) (*identityv1.ListOrgUnitsResponse, error) {
	// 根节点分页（children 不预填，前端懒加载）；租户隔离由 store 自动注入。
	items, meta, err := s.biz.ListOrgUnits(ctx, req.GetPaging())
	if err != nil {
		return nil, err
	}
	out := make([]*identityv1.OrgUnit, 0, len(items))
	for _, m := range items {
		out = append(out, convert.OrgUnitToPB(m))
	}
	return &identityv1.ListOrgUnitsResponse{Items: out, Meta: meta}, nil
}

func (s *orgUnitService) ListOrgUnitChildren(ctx context.Context, req *identityv1.ListOrgUnitChildrenRequest) (*identityv1.ListOrgUnitChildrenResponse, error) {
	items, meta, err := s.biz.ListOrgUnitChildren(ctx, tenantIDFromCtx(ctx), req.GetParentId(), req.GetPaging())
	if err != nil {
		return nil, err
	}
	out := make([]*identityv1.OrgUnit, 0, len(items))
	for _, m := range items {
		out = append(out, convert.OrgUnitToPB(m))
	}
	return &identityv1.ListOrgUnitChildrenResponse{Items: out, Meta: meta}, nil
}

func (s *orgUnitService) CountOrgUnits(ctx context.Context, req *identityv1.CountOrgUnitsRequest) (*identityv1.CountOrgUnitsResponse, error) {
	n, err := s.biz.CountOrgUnits(ctx, tenantIDFromCtx(ctx))
	if err != nil {
		return nil, err
	}
	return &identityv1.CountOrgUnitsResponse{Total: uint32(n)}, nil
}

func (s *orgUnitService) GetOrgUnit(ctx context.Context, req *identityv1.GetOrgUnitRequest) (*identityv1.GetOrgUnitResponse, error) {
	m, err := s.biz.GetOrgUnit(ctx, tenantIDFromCtx(ctx), req.GetCode())
	if err != nil {
		return nil, notFoundOr(err, "org/not_found", "org unit")
	}
	return &identityv1.GetOrgUnitResponse{OrgUnit: convert.OrgUnitToPB(m)}, nil
}

func (s *orgUnitService) CreateOrgUnit(ctx context.Context, req *identityv1.CreateOrgUnitRequest) (*identityv1.CreateOrgUnitResponse, error) {
	m, err := s.biz.CreateOrgUnit(ctx, tenantIDFromCtx(ctx), orgbiz.OrgUnit{
		Code: req.GetCode(), Name: req.GetName(),
		Type: req.GetType().String(), ParentID: req.GetParentId(),
		Status: req.GetStatus().String(), SortOrder: req.GetSortOrder(),
		LeaderID: req.GetLeaderId(), Remark: req.GetRemark(),
		Description: req.GetDescription(),
	})
	if err != nil {
		return nil, err // 校验→400、冲突→409、内部→500（ErrorInterceptor 收口）
	}
	return &identityv1.CreateOrgUnitResponse{OrgUnit: convert.OrgUnitToPB(m)}, nil
}

func (s *orgUnitService) UpdateOrgUnit(ctx context.Context, req *identityv1.UpdateOrgUnitRequest) (*identityv1.UpdateOrgUnitResponse, error) {
	in := orgbiz.OrgUnit{
		Name: req.GetName(), ParentID: req.GetParentId(),
		LeaderID: req.GetLeaderId(), Remark: req.GetRemark(),
		Description: req.GetDescription(),
	}
	// 枚举零值 = 未指定 → 不改（proto3 无「显式 null」语义，同 gin 侧）。
	if req.GetType() != identityv1.OrgUnit_TYPE_UNSPECIFIED {
		in.Type = req.GetType().String()
	}
	if req.GetStatus() != identityv1.OrgUnit_STATUS_UNSPECIFIED {
		in.Status = req.GetStatus().String()
	}
	if req.GetSortOrderSet() {
		in.SortOrder = req.GetSortOrder()
	}
	if err := s.biz.UpdateOrgUnit(ctx, tenantIDFromCtx(ctx), req.GetCode(), in); err != nil {
		return nil, notFoundOr(err, "org/not_found", "org unit") // 校验→400、环→400、内部→500
	}
	// 与 gin 侧同构：更新后回读，返回完整对象（含回填的 leader_name）。
	m, err := s.biz.GetOrgUnit(ctx, tenantIDFromCtx(ctx), req.GetCode())
	if err != nil {
		return nil, notFoundOr(err, "org/not_found", "org unit")
	}
	return &identityv1.UpdateOrgUnitResponse{OrgUnit: convert.OrgUnitToPB(m)}, nil
}

func (s *orgUnitService) DeleteOrgUnit(ctx context.Context, req *identityv1.DeleteOrgUnitRequest) (*identityv1.DeleteOrgUnitResponse, error) {
	if err := s.biz.DeleteOrgUnit(ctx, tenantIDFromCtx(ctx), req.GetCode()); err != nil {
		return nil, notFoundOr(err, "org/not_found", "org unit")
	}
	return &identityv1.DeleteOrgUnitResponse{Deleted: req.GetCode()}, nil
}

func (s *orgUnitService) BatchCreateOrgUnits(ctx context.Context, req *identityv1.BatchCreateOrgUnitsRequest) (*identityv1.BatchCreateOrgUnitsResponse, error) {
	tid := tenantIDFromCtx(ctx)
	ins := make([]orgbiz.OrgUnit, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		ins = append(ins, orgbiz.OrgUnit{
			Code: it.GetCode(), Name: it.GetName(),
			Type: it.GetType().String(), ParentID: it.GetParentId(),
			Status: it.GetStatus().String(), SortOrder: it.GetSortOrder(),
			LeaderID: it.GetLeaderId(), Remark: it.GetRemark(),
			Description: it.GetDescription(),
		})
	}
	created, failed := s.biz.BatchCreateOrgUnits(ctx, tid, ins)
	codes := make([]string, 0, len(created))
	for _, m := range created {
		codes = append(codes, m.Code)
	}
	return &identityv1.BatchCreateOrgUnitsResponse{Created: codes, Failed: failed}, nil
}

// ---- position ----

// positionService 实现生成的 identityv1.PositionServiceServer。
type positionService struct {
	identityv1.UnimplementedPositionServiceServer
	biz *orgbiz.Biz
}

// NewPositionServer 构造 PositionServiceServer 实现（biz 由 wire 装配注入）。
func NewPositionServer(biz *orgbiz.Biz) identityv1.PositionServiceServer {
	return &positionService{biz: biz}
}

func (s *positionService) ListPositions(ctx context.Context, req *identityv1.ListPositionsRequest) (*identityv1.ListPositionsResponse, error) {
	items, err := s.biz.ListPositions(ctx, tenantIDFromCtx(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]*identityv1.Position, 0, len(items))
	for _, m := range items {
		out = append(out, convert.PositionToPB(m))
	}
	return &identityv1.ListPositionsResponse{Items: out, Total: uint32(len(out))}, nil
}

func (s *positionService) CountPositions(ctx context.Context, req *identityv1.CountPositionsRequest) (*identityv1.CountPositionsResponse, error) {
	n, err := s.biz.CountPositions(ctx, tenantIDFromCtx(ctx))
	if err != nil {
		return nil, err
	}
	return &identityv1.CountPositionsResponse{Total: uint32(n)}, nil
}

func (s *positionService) GetPosition(ctx context.Context, req *identityv1.GetPositionRequest) (*identityv1.GetPositionResponse, error) {
	m, err := s.biz.GetPosition(ctx, tenantIDFromCtx(ctx), req.GetCode())
	if err != nil {
		return nil, notFoundOr(err, "org/not_found", "position")
	}
	return &identityv1.GetPositionResponse{Position: convert.PositionToPB(m)}, nil
}

func (s *positionService) CreatePosition(ctx context.Context, req *identityv1.CreatePositionRequest) (*identityv1.CreatePositionResponse, error) {
	m, err := s.biz.CreatePosition(ctx, tenantIDFromCtx(ctx), positionFromCreateGRPC(req))
	if err != nil {
		return nil, err
	}
	return &identityv1.CreatePositionResponse{Position: convert.PositionToPB(m)}, nil
}

func (s *positionService) UpdatePosition(ctx context.Context, req *identityv1.UpdatePositionRequest) (*identityv1.UpdatePositionResponse, error) {
	in := orgbiz.Position{
		Name: req.GetName(), Remark: req.GetRemark(),
		Description: req.GetDescription(), JobFamily: req.GetJobFamily(),
		JobGrade: req.GetJobGrade(), OrgUnitID: req.GetOrgUnitId(),
		ReportsToPositionID: req.GetReportsToPositionId(),
	}
	// 枚举零值 = 未指定 → 不改；标量经 *_set 显式位区分「未设置」与「设为零值」。
	if req.GetType() != identityv1.Position_TYPE_UNSPECIFIED {
		in.Type = req.GetType().String()
	}
	if req.GetStatus() != identityv1.Position_STATUS_UNSPECIFIED {
		in.Status = req.GetStatus().String()
	}
	if req.GetHeadcountSet() {
		in.Headcount = req.GetHeadcount()
	}
	if req.GetSortOrderSet() {
		in.SortOrder = req.GetSortOrder()
	}
	if req.GetLevelSet() {
		in.Level = req.GetLevel()
	}
	if req.GetIsKeyPositionSet() {
		in.IsKeyPosition = req.GetIsKeyPosition()
	}
	if err := s.biz.UpdatePosition(ctx, tenantIDFromCtx(ctx), req.GetCode(), in); err != nil {
		return nil, notFoundOr(err, "org/not_found", "position")
	}
	m, err := s.biz.GetPosition(ctx, tenantIDFromCtx(ctx), req.GetCode())
	if err != nil {
		return nil, notFoundOr(err, "org/not_found", "position")
	}
	return &identityv1.UpdatePositionResponse{Position: convert.PositionToPB(m)}, nil
}

func (s *positionService) DeletePosition(ctx context.Context, req *identityv1.DeletePositionRequest) (*identityv1.DeletePositionResponse, error) {
	if err := s.biz.DeletePosition(ctx, tenantIDFromCtx(ctx), req.GetCode()); err != nil {
		return nil, notFoundOr(err, "org/not_found", "position")
	}
	return &identityv1.DeletePositionResponse{Deleted: req.GetCode()}, nil
}

func (s *positionService) BatchCreatePositions(ctx context.Context, req *identityv1.BatchCreatePositionsRequest) (*identityv1.BatchCreatePositionsResponse, error) {
	tid := tenantIDFromCtx(ctx)
	ins := make([]orgbiz.Position, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		ins = append(ins, orgbiz.Position{
			Code: it.GetCode(), Name: it.GetName(),
			Headcount: it.GetHeadcount(), SortOrder: it.GetSortOrder(),
			Status: it.GetStatus().String(), Type: it.GetType().String(),
			Remark: it.GetRemark(), Description: it.GetDescription(),
			JobFamily: it.GetJobFamily(), JobGrade: it.GetJobGrade(),
			Level: it.GetLevel(), IsKeyPosition: it.GetIsKeyPosition(),
			OrgUnitID: it.GetOrgUnitId(), ReportsToPositionID: it.GetReportsToPositionId(),
		})
	}
	created, failed := s.biz.BatchCreatePositions(ctx, tid, ins)
	codes := make([]string, 0, len(created))
	for _, m := range created {
		codes = append(codes, m.Code)
	}
	return &identityv1.BatchCreatePositionsResponse{Created: codes, Failed: failed}, nil
}

// ---- 转换（包内独立实现，与 gin 侧同构但不共享） ----

// positionFromCreateGRPC 把 CreatePositionRequest 转为 biz 入参。
func positionFromCreateGRPC(req *identityv1.CreatePositionRequest) orgbiz.Position {
	in := orgbiz.Position{
		Code: req.GetCode(), Name: req.GetName(),
		Headcount: req.GetHeadcount(), SortOrder: req.GetSortOrder(),
		Status: req.GetStatus().String(), Type: req.GetType().String(),
		Remark: req.GetRemark(), Description: req.GetDescription(),
		JobFamily: req.GetJobFamily(), JobGrade: req.GetJobGrade(),
		Level: req.GetLevel(), IsKeyPosition: req.GetIsKeyPosition(),
		OrgUnitID: req.GetOrgUnitId(), ReportsToPositionID: req.GetReportsToPositionId(),
	}
	if ts := req.GetStartAt(); ts != nil {
		t := ts.AsTime()
		in.StartAt = &t
	}
	return in
}

// 保留 berrors 引用（错误语义经 ErrorInterceptor 收口，此处仅类型契约声明）。
var _ = berrors.BadRequest
var _ = storev1.PagingRequest{}
