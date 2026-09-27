package gin

// language.go 语言管理 REST handler（Wave 5.3）。路由挂载遵循仓库双范式：
// 分组级 Authn（/v1 需登录）+ 路由级 Authz（P9 归一化权限点）。
// REST 路径用单数段（/v1/language），与 gRPC
// DefaultGRPCObject("LanguageService/...")="language" 同源——casbin 策略单写
// 即覆盖双协议。

import (
	"context"
	"net/http"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	mid "github.com/kalandramo/bald/pkg/middleware/gin"

	dictv1 "github.com/kalandramo/bald-admin/api/gen/go/dict/v1"
	langbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/language"
	convert "github.com/kalandramo/bald-admin/internal/apiserver/handler/convert"
)

// RegisterLanguage 挂载语言管理路由（全部需认证 + language 资源权限，
// admin 写 / viewer 读）。
//   - GET    /v1/language          列表（?enabled_only=true）
//   - GET    /v1/language/count    统计
//   - GET    /v1/language/:id      取单个
//   - POST   /v1/language          创建
//   - POST   /v1/language/batch    批量创建（源未实现 → 501）
//   - PUT    /v1/language/:id      更新
//   - DELETE /v1/language/:id      删除
func RegisterLanguage(
	e *gingonic.Engine,
	authenticator authn.Authenticator,
	authorizer authz.Authorizer,
	biz *langbiz.Biz,
) {
	authed := e.Group("/v1")
	authed.Use(authnMiddleware(authenticator))
	authzMW := mid.AuthzMiddleware(authorizer,
		mid.WithObjectResolver(authz.DefaultHTTPObject),
		mid.WithActionResolver(authz.DefaultHTTPAction),
	)

	authed.GET("/language", authzMW, func(c *gingonic.Context) {
		items, total, err := biz.List(c.Request.Context(), c.Query("enabled_only") == "true")
		if err != nil {
			writeBizErr(c, err)
			return
		}
		out := make([]*dictv1.Language, 0, len(items))
		for _, m := range items {
			out = append(out, convert.LanguageToPB(m))
		}
		writePB(c, http.StatusOK, &dictv1.ListLanguagesResponse{Items: out, Total: uint32(total)})
	})

	// 注意：/language/count 必须注册在 /language/:id **之前**——gin 的路由树
	// 对静态段优先，但显式排序可避免歧义（与 dict 的 /count 同款约定）。
	authed.GET("/language/count", authzMW, func(c *gingonic.Context) {
		n, err := biz.Count(c.Request.Context())
		if err != nil {
			writeBizErr(c, err)
			return
		}
		writePB(c, http.StatusOK, &dictv1.CountLanguagesResponse{Count: uint32(n)})
	})

	authed.GET("/language/:id", authzMW, func(c *gingonic.Context) {
		m, err := biz.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &dictv1.GetLanguageResponse{Language: convert.LanguageToPB(m)})
	})

	authed.POST("/language", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusCreated,
			func(ctx context.Context, req *dictv1.CreateLanguageRequest) (*dictv1.CreateLanguageResponse, error) {
				m, err := biz.Create(ctx, langbiz.Language{
					ID: req.GetId(), LanguageName: req.GetLanguageName(),
					NativeName: req.GetNativeName(), IsDefault: req.GetIsDefault(),
					IsEnabled: req.GetIsEnabled(), SortOrder: req.GetSortOrder(),
				})
				if err != nil {
					return nil, err // 校验→400、冲突→409、内部→500
				}
				return &dictv1.CreateLanguageResponse{Language: convert.LanguageToPB(m)}, nil
			})
	})

	authed.POST("/language/batch", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *dictv1.BatchCreateLanguagesRequest) (*dictv1.BatchCreateLanguagesResponse, error) {
				ins := make([]langbiz.Language, 0, len(req.GetItems()))
				for _, it := range req.GetItems() {
					ins = append(ins, langbiz.Language{
						ID: it.GetId(), LanguageName: it.GetLanguageName(),
						NativeName: it.GetNativeName(), IsDefault: it.GetIsDefault(),
						IsEnabled: it.GetIsEnabled(), SortOrder: it.GetSortOrder(),
					})
				}
				ids, err := biz.BatchCreate(ctx, ins)
				if err != nil {
					return nil, err // ErrUnimplemented → 501
				}
				return &dictv1.BatchCreateLanguagesResponse{CreatedIds: ids}, nil
			})
	})

	authed.PUT("/language/:id", authzMW, func(c *gingonic.Context) {
		handlePB(c, http.StatusOK,
			func(ctx context.Context, req *dictv1.UpdateLanguageRequest) (*dictv1.UpdateLanguageResponse, error) {
				in := langbiz.UpdateInput{}
				if req.GetLanguageName() != "" {
					v := req.GetLanguageName()
					in.LanguageName = &v
				}
				if req.GetNativeName() != "" {
					v := req.GetNativeName()
					in.NativeName = &v
				}
				if req.GetIsDefaultSet() {
					v := req.GetIsDefault()
					in.IsDefault = &v
				}
				if req.GetIsEnabledSet() {
					v := req.GetIsEnabled()
					in.IsEnabled = &v
				}
				if req.GetSortOrder() != 0 {
					v := req.GetSortOrder()
					in.SortOrder = &v
				}
				m, err := biz.Update(ctx, c.Param("id"), in)
				if err != nil {
					return nil, err // NotFound→404、内部→500
				}
				return &dictv1.UpdateLanguageResponse{Language: convert.LanguageToPB(m)}, nil
			})
	})

	authed.DELETE("/language/:id", authzMW, func(c *gingonic.Context) {
		deleted, err := biz.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeBizErr(c, err) // NotFound→404、内部→500
			return
		}
		writePB(c, http.StatusOK, &dictv1.DeleteLanguageResponse{Deleted: deleted})
	})
}
