// Package language 是语言管理业务（Wave 5.3，自 go-wind-admin dict 域 language
// 精简移植，7 rpc）。
//
// ## 平台级数据（无租户隔离）
//
// 源 `language.proto` 无 tenant_id，语言是**平台级**共享数据（所有租户共用），
// 故本 biz 的查询**不走** `Where.T` 租户过滤（与同目录 dict 的租户级隔离不同）
// ——这是对齐源语义。
//
// ## 业务语义（对齐源）
//
//   - 主键 = 语言代码（如 "zh-CN"），Create 冲突 → 409；
//   - List 按 sort_order 升序（源语义「值越小越靠前」）；
//   - Update 用「非空字段即更新」语义（源用 FieldMask，本项目惯例见 proto 头注）；
//   - BatchCreate **源未实现** → UNIMPLEMENTED（对齐源，不自创）。
package language

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/store"

	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// ErrValidation 入参校验失败（handler 归 400）。
var ErrValidation = errors.New("language: validation failed")

// ErrNotFound 语言不存在。
var ErrNotFound = store.ErrNotFound

// ErrConflict 语言代码已存在。
var ErrConflict = store.ErrConflict

// Biz 语言管理业务。
type Biz struct{}

// New 构造语言业务。
func New() *Biz { return &Biz{} }

// langStore 请求期读取（InitBridges 装配后可用）。
func (b *Biz) langStore() *store.Store[authmodel.Language] {
	return bootstrappkg.LanguageStore
}

// List 列出语言（sort_order 升序，值小在前）。enabledOnly 为真时只返回启用的。
//
// 平台级数据：**不走** Where.T 租户过滤（源无租户维度）。
func (b *Biz) List(ctx context.Context, enabledOnly bool) ([]*authmodel.Language, int64, error) {
	w := &store.Where{}
	if enabledOnly {
		w.Filters = append(w.Filters, store.Eq("is_enabled", "true"))
	}
	w.Sorting = append(w.Sorting, store.Sort("sort_order"))
	return b.langStore().List(ctx, w)
}

// Get 取单个语言（按语言代码）。
func (b *Biz) Get(ctx context.Context, id string) (*authmodel.Language, error) {
	if id == "" {
		return nil, berrors.BadRequest("language/invalid_request").WithMessage("language: id required")
	}
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", id))
	m, err := b.langStore().Get(ctx, w)
	if err != nil || m == nil {
		return nil, berrors.NotFound("language/not_found")
	}
	return m, nil
}

// Create 创建语言（id 即语言代码，业务键；重复 → conflict）。
func (b *Biz) Create(ctx context.Context, in Language) (*authmodel.Language, error) {
	if in.ID == "" || in.LanguageName == "" {
		return nil, berrors.BadRequest("language/invalid_request").
			WithMessage("language: id and language_name required")
	}
	m := &authmodel.Language{
		ID:           in.ID,
		LanguageName: in.LanguageName,
		NativeName:   in.NativeName,
		IsDefault:    in.IsDefault,
		IsEnabled:    in.IsEnabled,
		SortOrder:    in.SortOrder,
	}
	if err := b.langStore().Create(ctx, m); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, berrors.AlreadyExists("language/conflict").
				WithMessage("language already exists: %s", in.ID)
		}
		return nil, fmt.Errorf("language: create: %w", err)
	}
	return m, nil
}

// Update 更新语言（非空字段即更新；id 不可变）。
//
// 与源 FieldMask 的差异：本项目惯例用「显式 set 位」区分「未设置」与「设为
// false」（见 proto 的 is_default_set/is_enabled_set）。
func (b *Biz) Update(ctx context.Context, id string, in UpdateInput) (*authmodel.Language, error) {
	m, err := b.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.LanguageName != nil {
		m.LanguageName = *in.LanguageName
	}
	if in.NativeName != nil {
		m.NativeName = *in.NativeName
	}
	if in.IsDefault != nil {
		m.IsDefault = *in.IsDefault
	}
	if in.IsEnabled != nil {
		m.IsEnabled = *in.IsEnabled
	}
	if in.SortOrder != nil {
		m.SortOrder = *in.SortOrder
	}
	m.UpdatedAt = time.Now()
	if err := b.langStore().Update(ctx, m); err != nil {
		return nil, fmt.Errorf("language: update: %w", err)
	}
	return m, nil
}

// Delete 删除语言。返回被删除的语言代码。
func (b *Biz) Delete(ctx context.Context, id string) (string, error) {
	m, err := b.Get(ctx, id)
	if err != nil {
		return "", err
	}
	w := &store.Where{}
	w.Filters = append(w.Filters, store.Eq("id", m.ID))
	if err := b.langStore().Delete(ctx, w); err != nil {
		return "", fmt.Errorf("language: delete: %w", err)
	}
	return m.ID, nil
}

// Count 统计语言数量。
func (b *Biz) Count(ctx context.Context) (int64, error) {
	_, total, err := b.langStore().List(ctx, &store.Where{})
	if err != nil {
		return 0, fmt.Errorf("language: count: %w", err)
	}
	return total, nil
}

// BatchCreate 批量创建——**源 service 层未实现**（proto 有、实现无），
// 故本实现返回 UNIMPLEMENTED（对齐源行为，不自创语义）。
//
// 返回 berrors.Unimplemented（而非裸 error）——writeBizErr 只识别
// berrors.Error；裸 error 会被兜底成 500（实测踩坑），而 501 才是正确映射。
func (b *Biz) BatchCreate(ctx context.Context, items []Language) ([]string, error) {
	return nil, berrors.Unimplemented("language/unimplemented").
		WithMessage("language: batch create not implemented (source service also unimplemented)")
}

// Language 是创建入参。
type Language struct {
	ID           string
	LanguageName string
	NativeName   string
	IsDefault    bool
	IsEnabled    bool
	SortOrder    int32
}

// UpdateInput 是更新入参（nil = 不更新该字段）。
type UpdateInput struct {
	LanguageName *string
	NativeName   *string
	IsDefault    *bool
	IsEnabled    *bool
	SortOrder    *int32
}
