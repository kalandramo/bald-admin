package model

import "time"

// User 系统用户。Roles 以逗号分隔存储角色名（MVP 简化，避免独立关联表）。
type User struct {
	ID           string `gorm:"primaryKey"`  // 用户 ID（如 u-admin）
	Username     string `gorm:"uniqueIndex"` // 登录名
	PasswordHash string // bcrypt 哈希（M3 起，MVP 明文阶段已废弃）
	TenantID     string `gorm:"index"`
	Roles        string // 逗号分隔角色名，如 "admin" 或 "viewer"
	// 时间戳由 GORM 约定自动维护（CreatedAt 插入、UpdatedAt 每次更新）；T2 起对外暴露。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RolesList 解析 Roles 字段为角色名切片。
func (u User) RolesList() []string {
	return splitCSV(u.Roles)
}

// RoleSuperAdmin 是**平台级身份**的角色名（跨租户视图的显式载体）。
//
// 语义边界（重要）：
//   - 它**只标记身份**，不授予任何权限——casbin 的 p 行不因它增加。
//     要给它权限需另外加 RolePolicy 行（当前刻意不加，保持「平台身份」
//     与「权限等级」两个维度正交）。
//   - 对接点：auth biz 据此签发 AuthClaims.Platform，bald pkg/store 的
//     shouldIsolate 据此跳过租户隔离。**它不替代授权判定**（授权归 casbin）。
//
// 为什么定义在 model 而非 auth biz：bootstrap 需要用它播种角色，而
// bootstrap **不能** import auth biz（authbiz 已 import bootstrap，反向依赖
// 会成环——见 bootstrap.go 的包注释）。model 是双方共同依赖的叶子包。
const RoleSuperAdmin = "superadmin"

// IsPlatformUser 判定用户是否持有平台级身份（superadmin 角色）。
//
// 用于注入 authbiz.SetPlatformResolver（见 main.go 装配处）。
//
// 判据是**显式白名单**：仅当角色列表中含 superadmin 才返回 true——不基于
// 租户值、不基于用户名为空等隐式信号（那些会 fail-open，使空租户的匿名/
// 异常令牌获得跨租户能力）。
//
// 为什么遍历 RolesList 而非 strings.Contains：Roles 是逗号分隔值
// （"admin,superadmin"），直接 Contains 会让 "notsuperadmin" 误命中。
func IsPlatformUser(u *User) bool {
	if u == nil {
		return false
	}
	for _, r := range u.RolesList() {
		if r == RoleSuperAdmin {
			return true
		}
	}
	return false
}
