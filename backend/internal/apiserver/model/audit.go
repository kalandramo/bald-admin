package model

// AuditRecord 审计事件落库实体（M9 延伸：审计后端落库）。
// 与 User/Secret 不同，审计表刻意「全量记录」——不走现有 pkg/store 的 TenantID 自动过滤
// （那是读隔离语义，审计是写全量留痕）；TenantID 仅作为列存储，由审计查询方按需过滤。
type AuditRecord struct {
	ID       uint   `gorm:"primaryKey;autoIncrement"` // 自增主键
	TenantID string `gorm:"index"`                    // 租户（来自 AuditEvent.TenantID）
	Time     int64  `gorm:"index"`                    // 事件时间（UnixNano）
	Subject  string // 操作主体（来自 AuditEvent.Subject）
	Object   string // 资源对象（来自 AuditEvent.Object）
	Action   string // 操作动作（来自 AuditEvent.Action）
	Result   string // allow/deny/error（来自 AuditEvent.Result）
	Error    string // 错误详情（来自 AuditEvent.Error，空为成功）

	// T6 审计增强（自源 audit/service/v1 的 Operation/LoginAuditLog 精简，
	// 单表 + Category 分类而非双表；Success 由 Result 推导不冗余存储）。
	Category  string `gorm:"index"` // 分类："operation"/"login"/"api"/"data_access"/"permission"
	IPAddress string // 客户端 IP（Meta["client_ip"]，源 ip_address）
	UserAgent string // 客户端 UA（Meta["user_agent"]，源 device_info 精简）
	RequestID string // 全局请求 ID（Meta["request_id"]，关联网关日志）
	TraceID   string // W3C 链路 ID（Meta["trace_id"]）

	// ---- Wave 5.1：五类差异字段并集（nullable，各分类按需填充）----
	// 决策：单表 + category 承载源五类（源是五张独立表、字段差异大），
	// 取并集为可空列，避免拆 5 表带来的 model/store/handler 三重膨胀。
	// 能力验证重点是「五类能落库 + 能查询」，非表结构逐字对齐。

	// api 类专属（源 ApiAuditLog）。
	HTTPMethod string // HTTP 方法（Meta["http_method"]）
	Path       string // 请求路径（Meta["path"]）
	StatusCode uint32 // HTTP 响应状态码（Meta["status"]）
	LatencyMs  uint32 // 处理耗时毫秒（Meta["latency_ms"]）

	// data_access 类专属（源 DataAccessAuditLog）。
	TableName    string // 被访问表名（Meta["table_name"]）
	DataSource   string // 数据源标识（Meta["data_source"]）
	DBUser       string // 数据库用户（Meta["db_user"]）
	SQLText      string // 脱敏 SQL 文本（Meta["sql_text"]）
	AffectedRows uint32 // 影响行数（Meta["affected_rows"]）

	// permission 类专属（源 PermissionAuditLog）。
	TargetType string // 变更目标类型（Meta["target_type"]）
	TargetID   string // 变更目标 ID（Meta["target_id"]）
	OldValue   string // 变更前值（Meta["old_value"]）
	NewValue   string // 变更后值（Meta["new_value"]）

	// login 类专属（源 LoginAuditLog）。
	SessionID string // 会话 ID（Meta["session_id"]）
	MFAStatus string // MFA 状态（Meta["mfa_status"]）
}
