// Package sqlaudit 提供 data_access 类审计的 gorm 采集插件（Wave 5.2）。
//
// ## 源语义与移植差异（关键，必须记录）
//
// 源 go-wind-admin 的 `audit_driver_wrapper.go` 包装的是 **ent 的
// `dialect.Driver`**（ent 框架的驱动接口），在 Exec/Query 前后采集 SQL 事件，
// 写 `DataAccessAuditLog`（table_name / sql_text / db_user / affected_rows）。
//
// **bald-admin 用 gorm 而非 ent**——不能照搬 driver 包装。gorm 生态的正确姿势是
// **Callback 机制**（`db.Callback().Query()/Create()/Update()/Delete()` 注册
// 处理器），在 SQL 执行前后采集。这是「同一能力轴、不同 ORM 的正确实现」，
// 本身是一次有价值的框架/生态验证。
//
// ## 防重入（最关键的约束）
//
// Callback 会拦截**所有** DB 操作——包括审计表自身写入。若不防重入，写审计
// 记录 → 触发 callback → 再写审计记录 → 无限递归。
//
// 源用 `audit.IsSinking(ctx)` 标记「正在落库审计」的上下文。本实现用同样的
// context 标记法：审计落库期间在 ctx 上打标，callback 见到标记即跳过。
//
// ## 采集范围（称量）
//
// 只采集**写操作**（Create/Update/Delete）与**显式查询**（Query）——与源
// `collectMasked` 的 isWrite 区分一致。SQL 文本**脱敏**（字面量替换为 ?），
// 避免把敏感数据写进审计表。
package sqlaudit

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/kalandramo/bald/pkg/audit"
)

// nowFn 取当前时间（可在测试中替换）。
var nowFn = time.Now

// sinkKey 是 context 标记键（审计落库进行中）。
type sinkKey struct{}

// WithSinking 在 ctx 上标记「审计落库进行中」，callback 见到即跳过（防重入）。
func WithSinking(ctx context.Context) context.Context {
	return context.WithValue(ctx, sinkKey{}, true)
}

// IsSinking 判断 ctx 是否处于审计落库中。
func IsSinking(ctx context.Context) bool {
	v, _ := ctx.Value(sinkKey{}).(bool)
	return v
}

// Register 把 SQL 采集回调注册到 gorm DB。
//
// db 须为业务主库（`bootstrappkg.DB`）。注册是幂等的（重复调用会覆盖同名
// callback，gorm 语义），但建议只调一次（在 InitBridges 或 main 装配期）。
//
// 采集的事件 category=data_access，Meta 含 table_name / sql_text /
// db_user / affected_rows / data_source。
func Register(db *gorm.DB) {
	if db == nil {
		return
	}
	// 在「SQL 执行后」采集（可拿到 RowsAffected）。
	_ = db.Callback().Query().After("gorm:query").Register("bald:sqlaudit:query", func(tx *gorm.DB) {
		collect(tx, "query", false)
	})
	_ = db.Callback().Create().After("gorm:create").Register("bald:sqlaudit:create", func(tx *gorm.DB) {
		collect(tx, "create", true)
	})
	_ = db.Callback().Update().After("gorm:update").Register("bald:sqlaudit:update", func(tx *gorm.DB) {
		collect(tx, "update", true)
	})
	_ = db.Callback().Delete().After("gorm:delete").Register("bald:sqlaudit:delete", func(tx *gorm.DB) {
		collect(tx, "delete", true)
	})
}

// collect 从一次 SQL 执行中采集 data_access 审计事件。
//
// 旁路语义：任何异常（无 ctx、sinking、无 SQL）都静默跳过，绝不 panic 或
// 影响业务 SQL 执行。
func collect(tx *gorm.DB, op string, isWrite bool) {
	if tx == nil || tx.Statement == nil {
		return
	}
	ctx := tx.Statement.Context
	if ctx == nil || IsSinking(ctx) {
		return // 防重入：审计落库自身的 SQL 不采集
	}
	sql := tx.Statement.SQL.String()
	if sql == "" {
		return
	}
	table := tx.Statement.Table
	if table == "audit_records" {
		return // 双保险：审计表自身永不采集（即便 ctx 标记失效）
	}

	ev := audit.AuditEvent{
		Time:   nowFn(),
		Object: "data_access",
		Action: op,
		Result: audit.ResultAllow,
		Meta: map[string]any{
			"category":      "data_access",
			"table_name":    table,
			"sql_text":      MaskSQL(sql),
			"data_source":   "gorm",
			"affected_rows": tx.RowsAffected,
		},
	}
	if isWrite {
		ev.Meta["is_write"] = true
	}
	recordSafely(ctx, ev)
}

// recordSafely 旁路记录：打上 sinking 标记后落库（防 callback 重入），
// panic 仅忽略。
func recordSafely(ctx context.Context, ev audit.AuditEvent) {
	defer func() { _ = recover() }()
	audit.GetAuditor().Record(WithSinking(ctx), ev)
}

// MaskSQL 对 SQL 文本做字面量脱敏（单引号字符串 → 单个 ?），避免把敏感
// 数据写进审计表。与源 `audit.MaskSQL` 同目的（本实现为简化版：仅处理
// 单引号字符串字面量）。
//
// 语义：一对引号（含内容）整体替换为**一个** `?`——`'alice'` → `?`。
func MaskSQL(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	for i := 0; i < len(sql); i++ {
		if sql[i] != '\'' {
			b.WriteByte(sql[i])
			continue
		}
		// 遇到开引号：整体（含内容与闭合引号）替换为单个 ?。
		b.WriteByte('?')
		for i+1 < len(sql) && sql[i+1] != '\'' {
			i++ // 跳过字符串内容
		}
		if i+1 < len(sql) {
			i++ // 跳过闭合引号
		}
	}
	return b.String()
}
