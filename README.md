# bald-admin

bald 框架的 reference example（前后端 monorepo）：用 bald 重构 go-wind-admin 的
管理后台，验证 P0–P9 框架能力在真实业务下的可用性。

由 `kalandramo/bald` 仓库 `examples/` 独立迁移而来（前后端合仓是契约同步的
前置约束——proto 生成物跨目录直出前端，只在同仓内持续成立）。

## 布局

```
backend/    Go API 服务（module github.com/kalandramo/bald-admin）
frontend/   Vue 3 管理前端（v3-admin-vite 定制，pnpm）
```

## 契约同步

proto 是前后端唯一契约真相源（`backend/api/protos/<域>/v1/*.proto`，带全
google.api.http 注解；api 根只放 buf 配置，module 边界在 protos/，与
go-wind-admin 分层对齐）。生成链路（全命令均在 `backend/api/` 下执行）：

1. Go 客户端：`buf generate` → `backend/api/gen/go/`（生成物按语言分层）
2. OpenAPI 面：`buf generate --template buf.openapi.gen.yaml` →
   `backend/internal/apiserver/assets/openapi.yaml`（OpenAPI v3，gnostic
   protoc-gen-openapi；`naming=proto` 保 snake_case 与后端 writePB 的
   protojson UseProtoNames 输出逐字段一致，`enum_type=string` 输出枚举名）。
   spec 由 `//go:embed` 编译进二进制，运行期 `GET /openapi.yaml` 可得
   （文档与代码版本零漂移；Swagger UI 不内嵌，Apifox/在线工具消费）
3. 前端 TS：`cd frontend && pnpm api:gen`（orval，tags 模式按 proto
   service 分文件 + axios-functions 平铺，读同一份 assets spec）→
   `frontend/src/api/generated/`

改 proto 后按 1→2→3 顺序重新生成，禁止手改生成物（assets/openapi.yaml
是生成物但入库——embed 编译期依赖）。

### 未契约化端点（手写保留）

- `POST /v1/login`、`GET /v1/auth/whoami`：gin 特例（无 proto 契约），
  见 `frontend/src/common/apis/auth/`
- `POST /v1/file/upload`（multipart + 进度）、`GET /v1/file/{id}/download`
  （blob 流式）：传输形态不进契约（gateway 面的 JSON base64/bytes 是另一
  形态），见 `frontend/src/common/apis/files/`

### 前端消费约定

- 业务域类型与函数全部 import 自 `@/api/generated/*`；错误体消费见
  `src/http/axios.ts`（message 展示 + `details[0].reason` 程序化出口）
- 后端枚举经 protojson 输出**枚举名**（如 `"STATUS_ON"`），前端用生成
  的枚举常量（`TenantStatus.STATUS_ON` 等）比对，勿用数字
- 生成类型与 DOM 全局名冲突时 import 别名（如 `File as V1File`）

## 开发

后端依赖已发布的 bald v0.2.0（含 bconf/transport/contrib 等 v0.1.0 子模块），
**clone 本仓库即可独立构建**，无需本地 bald 兄弟仓库。联调本地 bald 改动时
临时加 replace：`go mod edit -replace=github.com/kalandramo/bald=../../bald`。

```bash
# 后端
cd backend && go run ./cmd/go-bald-admin --config=configs/go-bald-admin.yaml

# 前端
cd frontend && pnpm install && pnpm dev
```

种子账号：admin/admin123、alice|bob/alice123。

## 验证

```bash
cd backend  && go build ./... && go test ./...
cd frontend && pnpm exec vue-tsc --noEmit && pnpm lint && pnpm test
```
