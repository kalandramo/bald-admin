# go-bald-admin-web

基于 Vue 3 + TypeScript + Element Plus + Vite 的 Bald 管理后台前端。

## 快速开始

```bash
pnpm i
pnpm dev
```

前端地址 http://localhost:3333 ，API 代理指向 http://localhost:8080 。

## 后端代理配置

在 `vite.config.ts` 中修改代理目标：

```ts
proxy: {
  "/v1": {
    target: "http://localhost:8080",
    changeOrigin: true
  }
}
```

## 默认账号

- 用户名：admin　密码：admin123（与后端 seed 种子账号一致，见 `bootstrap.go`）

## 功能清单

| 模块 | 路由 | 说明 |
| --- | --- | --- |
| 租户管理 | /tenants | 租户 CRUD（admin 专属） |
| 用户管理 | /users | 当前租户用户 CRUD（admin） |
| 组织架构 | /org、/org/positions | 组织单元树 + 岗位管理（admin） |
| 套餐配额 | /plans、/plans/modules、/plans/quotas | 套餐 + 模块 + 配额（admin） |
| 身份凭证 | /identity/credentials、/identity/policies | 用户凭证 + 登录策略（admin） |
| 多因素认证 | /mfa | MFA 状态 + 绑定验证器 + 设备管理（admin） |
| 站内消息 | /messages、/messages/inbox | 消息管理 + 我的收件箱（admin） |
| 任务调度 | /tasks | 定时任务列表 + 启停控制（admin） |
| 系统管理 | /system/menus、/system/permissions | 菜单 + 权限（admin） |
| 系统管理 | /system/languages、/system/cache-monitor | 语言管理 + Redis 缓存监控（admin） |
| 字典管理 | /dicts | 字典类型 + 条目（admin） |
| 文件管理 | /files | 上传 / 下载 / 删除（admin） |
| 审计日志 | /audits | 操作 / 登录日志查询（admin） |

## 权限说明

- 页面入口通过路由 `meta.roles` 控制，当前用 admin 角色过滤；
- 按钮级权限通过 `v-permission` 指令实现，但 whoami 尚未返回 `permissions` 字段（保留待后端补齐）；
- 角色策略修改后需后端重启或热重载策略才生效。

## 脚本

```bash
pnpm build        # vue-tsc 类型检查 + vite 打包
pnpm build:staging # 预发布环境打包
pnpm test         # vitest 单元测试
pnpm lint         # eslint 检查与格式化
pnpm api:gen      # 从后端 openapi.yaml 生成 TS client（orval）
```

## 契约生成链路

后端 `proto` → `buf generate`（生成 openapi.yaml）→ `orval`（生成 `src/api/generated/*.ts`）。
生成物禁止手改（`clean: true` 全量重生成）。改契约流程见 `orval.config.ts` 头注。
