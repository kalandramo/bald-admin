package gin

// dict.go 字典管理 REST handler（T4）。路由挂载遵循仓库双范式：
// 分组级 Authn（/v1 需登录）+ 路由级 Authz（P9 归一化权限点）。
// REST 路径用单数段（/v1/dict_type、/v1/dict_entry），与 gRPC
// DefaultGRPCObject("DictTypeService/...")="dict_type"、"dict_entry" 同源——
// casbin 策略单写即覆盖双协议。

import (
	"context"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"
	web "github.com/kalandramo/bald/transport/web"

	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterDict 挂载字典管理路由（全部需认证 + dict_type/dict_entry 资源权限，
// admin 写 / viewer 读）。
//   - GET    /v1/dict_type        类型列表
//   - GET    /v1/dict_type/:id    取类型
//   - POST   /v1/dict_type        创建类型
//   - PUT    /v1/dict_type/:id    更新类型
//   - DELETE /v1/dict_type/:id    删除类型（级联条目）
//   - GET    /v1/dict_entry       条目列表（?type_code= 走 Cache-Aside）
//   - GET    /v1/dict_entry/:id   取条目
//   - POST   /v1/dict_entry       创建条目
//   - PUT    /v1/dict_entry/:id   更新条目
//   - DELETE /v1/dict_entry/:id   删除条目
func RegisterDict(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *dictbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/dict_type", authzMW, func(c *gingonic.Context) {
		ts, total, err := biz.ListTypes(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		items := make([]*dictv1.DictType, 0, len(ts))
		for _, t := range ts {
			items = append(items, convert.DictTypeToPB(t))
		}
		writePB(c, http.StatusOK, &dictv1.ListDictTypesResponse{Items: items, Total: uint32(total)})
	})

	authed.GET("/dict_type/:id", authzMW, func(c *gingonic.Context) {
		t, err := biz.GetType(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &dictv1.GetDictTypeResponse{DictType: convert.DictTypeToPB(t)})
	})

	authed.POST("/dict_type", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *dictv1.CreateDictTypeRequest) (*dictv1.CreateDictTypeResponse, error) {
				t, err := biz.CreateType(ctx, req.GetId(), req.GetTypeName(), req.GetSortOrder(), req.GetRemark())
				if err != nil {
					return nil, err // 校验→400、业务键冲突→409、内部→500
				}
				return &dictv1.CreateDictTypeResponse{DictType: convert.DictTypeToPB(t)}, nil
			})
	})

	authed.PUT("/dict_type/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *dictv1.UpdateDictTypeRequest) (*dictv1.UpdateDictTypeResponse, error) {
				// 空值字段不改；sort_order/enabled 用显式 *_set 位（0/false 是合法值）。
				t, err := biz.UpdateType(ctx, c.Param("id"),
					req.GetTypeName(), req.GetSortOrder(), req.GetSortOrderSet(),
					req.GetEnabled(), req.GetEnabledSet(), req.GetRemark())
				if err != nil {
					return nil, err // 校验→400、NotFound→404、内部→500
				}
				return &dictv1.UpdateDictTypeResponse{DictType: convert.DictTypeToPB(t)}, nil
			})
	})

	authed.DELETE("/dict_type/:id", authzMW, func(c *gingonic.Context) {
		deleted, err := biz.DeleteType(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、级联/内部→500
			return
		}
		if deleted == 0 {
			web.ErrorResponse(c, berrors.NotFound("dict/type_not_found").WithMessage("dict type not found"))
			return
		}
		writePB(c, http.StatusOK, &dictv1.DeleteDictTypeResponse{Deleted: c.Param("id")})
	})

	authed.GET("/dict_entry", authzMW, func(c *gingonic.Context) {
		es, total, err := biz.ListEntries(c.Request.Context(), c.Query("type_code"))
		if err != nil {
			writeBizErr(c, err) // 类型不存在→404、内部→500（此前一刀切折叠 404）
			return
		}
		items := make([]*dictv1.DictEntry, 0, len(es))
		for _, e := range es {
			items = append(items, convert.DictEntryToPB(e))
		}
		writePB(c, http.StatusOK, &dictv1.ListDictEntriesResponse{Items: items, Total: uint32(total)})
	})

	authed.GET("/dict_entry/:id", authzMW, func(c *gingonic.Context) {
		e, err := biz.GetEntry(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &dictv1.GetDictEntryResponse{DictEntry: convert.DictEntryToPB(e)})
	})

	authed.POST("/dict_entry", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *dictv1.CreateDictEntryRequest) (*dictv1.CreateDictEntryResponse, error) {
				e, err := biz.CreateEntry(ctx, req.GetTypeCode(), req.GetValue(),
					req.GetLabel(), req.Numeric, req.GetSortOrder(), req.GetRemark())
				if err != nil {
					return nil, err // 校验/类型不存在→400/404、业务键冲突→409
				}
				return &dictv1.CreateDictEntryResponse{DictEntry: convert.DictEntryToPB(e)}, nil
			})
	})

	authed.PUT("/dict_entry/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *dictv1.UpdateDictEntryRequest) (*dictv1.UpdateDictEntryResponse, error) {
				// numeric 用 numeric_set 位区分「改回空值」与「不改」。
				e, err := biz.UpdateEntry(ctx, c.Param("id"),
					req.GetLabel(), req.Numeric, req.GetNumericSet(),
					req.GetSortOrder(), req.GetSortOrderSet(),
					req.GetEnabled(), req.GetEnabledSet(), req.GetRemark())
				if err != nil {
					return nil, err // NotFound→404、内部→500（不再折叠 400）
				}
				return &dictv1.UpdateDictEntryResponse{DictEntry: convert.DictEntryToPB(e)}, nil
			})
	})

	authed.DELETE("/dict_entry/:id", authzMW, func(c *gingonic.Context) {
		ok, err := biz.DeleteEntry(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		if !ok {
			web.ErrorResponse(c, berrors.NotFound("dict/entry_not_found").WithMessage("dict entry not found"))
			return
		}
		writePB(c, http.StatusOK, &dictv1.DeleteDictEntryResponse{Deleted: c.Param("id")})
	})
}
