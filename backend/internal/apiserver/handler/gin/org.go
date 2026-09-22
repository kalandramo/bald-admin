// Package gin 提供组织架构域的 HTTP handler。
//
// Wave P0.5：本域从 B 轨退化形态（Go DTO + encoding/json）迁移为 A 轨标准形态
// （bindPB/writePB + protojson）——响应形状由 proto 契约（api/protos/identity/v1）
// 决定，与 gRPC/gateway 三面同源。
//
// 迁移要点：
//   - 请求体：bindPB（protojson，DiscardUnknown 兼容宽松客户端）
//   - 响应体：writePB（protojson UseProtoNames，snake_case 与前端建模一致）
//   - 错误出口：writeBizErr（框架统一 ErrorResponse，决策⑧结构）
//   - 模型 → PB：toOrgUnitPB / toPositionPB 转换函数
package gin

import (
	"net/http"
	"strconv"

	gingonic "github.com/gin-gonic/gin"
	"google.golang.org/protobuf/types/known/timestamppb"

	storev1 "github.com/kalandramo/bald/bconf/gen/go/bald/store/v1"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	identityv1 "github.com/kalandramo/bald-admin/api/gen/go/identity/v1"
	orgbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/org"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
)

// RegisterOrg 挂载组织架构路由（org_unit 7 rpc，树形 + position 7 rpc，关联回填）。
//
// 路由与 gRPC DefaultGRPCObject 同源（P9 归一化权限点）：
//   - GET    /v1/org-units            列表（树形）
//   - GET    /v1/org-units/count      统计
//   - GET    /v1/org-units/:code      取单个
//   - POST   /v1/org-units            创建
//   - PUT    /v1/org-units/:code      更新
//   - DELETE /v1/org-units/:code      删除
//   - POST   /v1/org-units/batch      批量创建
//   - position 同构（/v1/positions...）
func RegisterOrg(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *orgbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)
	tid := func(c *gingonic.Context) string {
		if claims := authn.AuthClaimsFromContext(c.Request.Context()); claims != nil {
			return claims.TenantID
		}
		return ""
	}

	// ---- org_unit ----

	authed.POST("/org-units", authzMW, func(c *gingonic.Context) {
		var req identityv1.CreateOrgUnitRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
		m, err := biz.CreateOrgUnit(c.Request.Context(), tid(c), orgbiz.OrgUnit{
			Code: req.GetCode(), Name: req.GetName(),
			Type: req.GetType().String(), ParentID: req.GetParentId(),
			Status: req.GetStatus().String(), SortOrder: req.GetSortOrder(),
			LeaderID: req.GetLeaderId(), Remark: req.GetRemark(),
			Description: req.GetDescription(),
		})
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusCreated, &identityv1.CreateOrgUnitResponse{OrgUnit: toOrgUnitPB(m)})
	})

	authed.GET("/org-units", authzMW, func(c *gingonic.Context) {
		// GET 无 body，分页参数从 query 读（见 pagingFromQuery 的踩坑说明）。
		// items = **根节点分页**（children 不预填，由前端 el-table 懒加载组装）；
		// meta.total = 根节点总数（非全量节点数——语义变更，见 proto 头注）。
		items, meta, err := biz.ListOrgUnits(c.Request.Context(), pagingFromQuery(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		out := make([]*identityv1.OrgUnit, 0, len(items))
		for _, n := range items {
			out = append(out, toOrgUnitPB(n))
		}
		writePB(c, http.StatusOK, &identityv1.ListOrgUnitsResponse{Items: out, Meta: meta})
	})

	// 懒加载：展开节点时拉取其直接子节点（el-table lazy/load）。
	// 注意：/org-units/:code/children 与 /org-units/:code 不冲突（gin 路由树按段匹配），
	// 但注册在 :code 之前更清晰（同 /count 的显式排序约定）。
	authed.GET("/org-units/:code/children", authzMW, func(c *gingonic.Context) {
		parentCode := c.Param("code")
		items, meta, err := biz.ListOrgUnitChildren(c.Request.Context(), tid(c), parentCode, pagingFromQuery(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		out := make([]*identityv1.OrgUnit, 0, len(items))
		for _, n := range items {
			out = append(out, toOrgUnitPB(n))
		}
		writePB(c, http.StatusOK, &identityv1.ListOrgUnitChildrenResponse{Items: out, Meta: meta})
	})

	// 注意：/org-units/count 必须注册在 /org-units/:code **之前**——gin 的路由树
	// 对静态段优先，显式排序避免歧义（同 dict/language 的 /count 约定）。
	authed.GET("/org-units/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.CountOrgUnits(c.Request.Context(), tid(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.CountOrgUnitsResponse{Total: uint32(n)})
	})

	authed.GET("/org-units/:code", authzMW, func(c *gingonic.Context) {
		m, err := biz.GetOrgUnit(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &identityv1.GetOrgUnitResponse{OrgUnit: toOrgUnitPB(m)})
	})

	authed.PUT("/org-units/:code", authzMW, func(c *gingonic.Context) {
		var req identityv1.UpdateOrgUnitRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
		in := orgbiz.OrgUnit{
			Name: req.GetName(), ParentID: req.GetParentId(),
			LeaderID: req.GetLeaderId(), Remark: req.GetRemark(),
			Description: req.GetDescription(),
		}
		// 枚举零值 = 未指定 → 不改（proto3 无「显式 null」语义）。
		if req.GetType() != identityv1.OrgUnit_TYPE_UNSPECIFIED {
			in.Type = req.GetType().String()
		}
		if req.GetStatus() != identityv1.OrgUnit_STATUS_UNSPECIFIED {
			in.Status = req.GetStatus().String()
		}
		if req.GetSortOrderSet() {
			in.SortOrder = req.GetSortOrder()
		}
		if err := biz.UpdateOrgUnit(c.Request.Context(), tid(c), c.Param("code"), in); err != nil {
			writeBizErr(c, err) // 校验→400、环→400、内部→500
			return
		}
		m, err := biz.GetOrgUnit(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.UpdateOrgUnitResponse{OrgUnit: toOrgUnitPB(m)})
	})

	authed.DELETE("/org-units/:code", authzMW, func(c *gingonic.Context) {
		code := c.Param("code")
		if err := biz.DeleteOrgUnit(c.Request.Context(), tid(c), code); err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.DeleteOrgUnitResponse{Deleted: code})
	})

	authed.POST("/org-units/batch", authzMW, func(c *gingonic.Context) {
		var req identityv1.BatchCreateOrgUnitsRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
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
		created, failed := biz.BatchCreateOrgUnits(c.Request.Context(), tid(c), ins)
		createdCodes := make([]string, 0, len(created))
		for _, m := range created {
			createdCodes = append(createdCodes, m.Code)
		}
		writePB(c, http.StatusOK, &identityv1.BatchCreateOrgUnitsResponse{
			Created: createdCodes, Failed: failed,
		})
	})

	// ---- position ----

	authed.POST("/positions", authzMW, func(c *gingonic.Context) {
		var req identityv1.CreatePositionRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
		m, err := biz.CreatePosition(c.Request.Context(), tid(c), positionFromCreate(&req))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusCreated, &identityv1.CreatePositionResponse{Position: toPositionPB(m)})
	})

	authed.GET("/positions", authzMW, func(c *gingonic.Context) {
		items, err := biz.ListPositions(c.Request.Context(), tid(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		out := make([]*identityv1.Position, 0, len(items))
		for _, m := range items {
			out = append(out, toPositionPB(m))
		}
		writePB(c, http.StatusOK, &identityv1.ListPositionsResponse{Items: out, Total: uint32(len(out))})
	})

	authed.GET("/positions/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.CountPositions(c.Request.Context(), tid(c))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.CountPositionsResponse{Total: uint32(n)})
	})

	authed.GET("/positions/:code", authzMW, func(c *gingonic.Context) {
		m, err := biz.GetPosition(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.GetPositionResponse{Position: toPositionPB(m)})
	})

	authed.PUT("/positions/:code", authzMW, func(c *gingonic.Context) {
		var req identityv1.UpdatePositionRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
		in := orgbiz.Position{
			Name: req.GetName(), Remark: req.GetRemark(),
			Description: req.GetDescription(), JobFamily: req.GetJobFamily(),
			JobGrade: req.GetJobGrade(), OrgUnitID: req.GetOrgUnitId(),
			ReportsToPositionID: req.GetReportsToPositionId(),
		}
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
		if err := biz.UpdatePosition(c.Request.Context(), tid(c), c.Param("code"), in); err != nil {
			writeBizErr(c, err)
			return
		}
		m, err := biz.GetPosition(c.Request.Context(), tid(c), c.Param("code"))
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.UpdatePositionResponse{Position: toPositionPB(m)})
	})

	authed.DELETE("/positions/:code", authzMW, func(c *gingonic.Context) {
		code := c.Param("code")
		if err := biz.DeletePosition(c.Request.Context(), tid(c), code); err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &identityv1.DeletePositionResponse{Deleted: code})
	})

	authed.POST("/positions/batch", authzMW, func(c *gingonic.Context) {
		var req identityv1.BatchCreatePositionsRequest
		if err := bindPB(c, &req); err != nil {
			bindErr(c, err)
			return
		}
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
		created, failed := biz.BatchCreatePositions(c.Request.Context(), tid(c), ins)
		createdCodes := make([]string, 0, len(created))
		for _, m := range created {
			createdCodes = append(createdCodes, m.Code)
		}
		writePB(c, http.StatusOK, &identityv1.BatchCreatePositionsResponse{
			Created: createdCodes, Failed: failed,
		})
	})
}

// pagingFromQuery 从 URL query 构造 PagingRequest（GET 列表用）。
//
// 为什么不能靠 bindPB：bindPB 只读 request **body**（pb.go:26-37），GET 请求
// 无 body，故分页参数不会被绑定——实测踩坑：page_size=2 时仍返回全部
// （meta.page_size=10 默认值）。项目既有惯例是 GET 手工读 query
// （见 audit.go 的 c.Query("page_size")），本函数把该惯例收敛为一处。
//
// 参数命名以 **grpc-gateway 约定**为准（嵌套消息字段展开为点号路径，见
// assets/openapi.yaml 生成的 `paging.page_size` 等）——这样直连 gin 与经
// gateway 转码的请求**参数名一致**，前端一套参数两处通用。为兼容手工调用
// （curl/调试）同时接受扁平别名。
func pagingFromQuery(c *gingonic.Context) *storev1.PagingRequest {
	req := &storev1.PagingRequest{}
	// queryAny 依次尝试多个候选参数名，返回首个非空值。
	queryAny := func(names ...string) string {
		for _, n := range names {
			if v := c.Query(n); v != "" {
				return v
			}
		}
		return ""
	}

	// 页码分页：给 page_size 时按 Page 策略（detectStrategy 优先级：
	// NoPaging > Token > Page > Offset > 默认页码）。
	if ps := queryAny("paging.page_size", "page_size"); ps != "" {
		if n, err := strconv.ParseUint(ps, 10, 32); err == nil && n > 0 {
			req.Page = uint32Ptr(1)
			req.PageSize = uint32Ptr(uint32(n))
		}
	}
	if pg := queryAny("paging.page", "page"); pg != "" {
		if n, err := strconv.ParseUint(pg, 10, 32); err == nil && n > 0 {
			req.Page = uint32Ptr(uint32(n))
		}
	}
	// 游标分页：token 优先于 page（见 detectStrategy 优先级）。
	if tok := queryAny("paging.token", "page_token", "token"); tok != "" {
		req.Token = strPtr(tok)
	}
	if off := queryAny("paging.offset", "offset"); off != "" {
		if n, err := strconv.ParseUint(off, 10, 64); err == nil {
			req.Offset = uint64Ptr(n)
		}
	}
	if lim := queryAny("paging.limit", "limit"); lim != "" {
		if n, err := strconv.ParseUint(lim, 10, 32); err == nil {
			req.Limit = uint32Ptr(uint32(n))
		}
	}
	if queryAny("paging.no_paging", "no_paging") == "true" {
		req.NoPaging = boolPtr(true)
	}
	if ob := queryAny("paging.order_by", "order_by"); ob != "" {
		req.OrderBy = strPtr(ob)
	}
	return req
}

func uint32Ptr(v uint32) *uint32 { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }
func strPtr(v string) *string    { return &v }
func boolPtr(v bool) *bool       { return &v }

// positionFromCreate 把 CreatePositionRequest 转为 biz 入参。
func positionFromCreate(req *identityv1.CreatePositionRequest) orgbiz.Position {
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

// toOrgUnitPB 模型 → proto。
func toOrgUnitPB(m *orgbiz.OrgUnit) *identityv1.OrgUnit {
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
			pb.Children = append(pb.Children, toOrgUnitPB(ch))
		}
	}
	return pb
}

// toPositionPB 模型 → proto。
func toPositionPB(m *orgbiz.Position) *identityv1.Position {
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

var _ = authmodel.OrgUnit{} // 保持 model 引用（转换层依赖其字段语义）
