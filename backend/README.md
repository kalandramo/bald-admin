# bald-admin (backend)

bald 框架的参考实现：用真实业务（用户/角色/机密、RBAC 授权、多租户隔离）跑通
bald 的核心能力——认证（JWT）、授权（casbin RBAC，REST/gRPC 同源归一化）、
审计（旁路不阻断）、可观测性指标（Prometheus 本地 + OTLP 远端直推）。全部经
bald 中间件/桥接实现，业务不直连引擎。

> 设计见 [`docs/设计文档.md`](docs/设计文档.md)；需求见 [`docs/需求文档.md`](docs/需求文档.md)。
> 实现契约（外部依赖禁止 fake/mock/stub）见设计文档 §0。
>
> **与 bald codegen 的关系**：`appspec.json` 是本应用的 AppSpec 声明——
> 12 个有 proto service 的域在此登记，域清单 `internal/domainset/domains.gen.go`
> 由它派生（`task gen:domain`；CI 用 `task gen:check` 守一致性）。装配骨架
> （servers / capability / 审计协调 / 配置 flag）与业务层为手写：本应用的装配面
> 承载了生成器不产出的业务钩子（BeforeStart 桥接、组件工厂、管理面），故未整体
> 套用 `gen app` 脚手架。

## 域声明（appspec.json）

域清单的唯一真相源。新增/删除域：

1. 写 proto（`api/protos/<域>/v1/`，含 `service`）与域声明包
   `internal/apiserver/domains/<域>d`（导出 `var Domain` 与 `Setup(Deps)`）；
2. 在 `appspec.json` 的 `domains[]` 登记 name / protoPackage / service / goPackage；
3. `task gen:domain` 重写 `internal/domainset/domains.gen.go`，并在
   `cmd/bald-admin/app/assembly.go` 的 Setup 段注入该域依赖（生成物只列清单，
   依赖接线仍是显式的）。

没有 gRPC service 的纯 gin 域（auth / mfa / identity / task / message /
dashboard / permgroup / plan）不纳入 AppSpec——`gen domain` 的 Gate1 要求
`domains.service` 与 proto 匹配，这些域继续在 `appOptions` 手写声明。

```bash
task gen:domain   # 按 appspec.json 重写域清单（跑在仓库内固定的 bald 版本上）
task gen:check    # 零写入校验清单与声明一致（CI 门禁）
```

## 架构

- 双协议：`gin` HTTP(`:8080`) + `gRPC`(`:9090`)，`grpc-gateway` REST 转码(`:8081`)。
- 认证：`bald-authn-jwt`（RS256/ES256/HMAC）。
- 授权：`bald-authz-casbin`（contrib）桥接 casbin；策略数据化——p 行落 `RolePolicy`
  表、g 行由 `User.Roles` 装载，启动期从 PG 读取（改库重启生效），REST/gRPC 同源
  归一化。
- 存储：`bald-store-gorm` 云端 PostgreSQL（无外部 DB 时回退 SQLite 内存库），
  AutoMigrate 自建表。
- 多租户：store 层自动注入 `tenant_id` + 租户 CRUD 的业务双层语义。
- 缓存：`bald-cache-redis`（contrib）Cache-Aside（Redis 停机/空配置自动降级直连 store）。
- 对象存储：`bald/oss/minio`（文件上传/下载，MIME 白名单 + SHA256 + 50MiB 上限 + 分桶）。
- 注册发现：`bald-registry-nacos`（契约装配 `RegistrarRegistry`，服务名带协议后缀）。
- 审计：`pkg/audit` 核心 + `internal/security/audit.MultiAuditor`（落库 + Redis Stream
  异步 + 日志降级，`audit.backends` 期望态热切换；登录/操作分类 + 分页查询）。
- 指标/trace：`pkg/metrics` 核心 + `bald-observability-otlp`（Prometheus(`:9091`)
  + OTLP 双通道）。
- 装配：`appkit` 负责框架发现与契约驱动配置（bconf proto 为唯一真相源），业务对象
  用普通 Go 函数构造（不引 wire，理由见设计文档 §1.1）。

## 运行

```bash
cd backend

# 真实配置不入库（含云端凭证）：首次使用先从模板复制并填入连接参数
cp configs/bald-admin.yaml.example configs/bald-admin.yaml

# 起服务（:8080 HTTP / :9090 gRPC / :8081 gateway / :9091 metrics）
go run ./cmd/bald-admin --config=configs/bald-admin.yaml

# 覆盖地址（三种等价手段，优先级 flag > env > yaml）
go run ./cmd/bald-admin --server.http.addr=:18080
BALD_ADMIN_SERVER_HTTP_ADDR=:18080 go run ./cmd/bald-admin
```

### 端口

| 端口 | 用途 | 覆盖手段 |
|------|------|----------|
| `:8080` | HTTP(gin) 主服务 | `--server.http.addr` / `BALD_ADMIN_SERVER_HTTP_ADDR` / yaml `server.http.addr` |
| `:9090` | gRPC | `--server.grpc.addr` / `BALD_ADMIN_SERVER_GRPC_ADDR` / yaml `server.grpc.addr` |
| `:8081` | grpc-gateway REST 转码 | yaml `server.gateway.addr`（无 flag；见下） |
| `:9091` | `/metrics` Prometheus 抓取 | yaml `metrics.prometheus.addr`（`type` 必填） |

> env 键名由 app name 规范化派生（`bald-admin` → `BALD_ADMIN_` 前缀，
> 下划线即点路径分隔：`BALD_ADMIN_SERVER_HTTP_ADDR` ⇔ `server.http.addr`）。
> metrics 缺省端口与 gRPC 错开（`:9091`）。

### 远端遥测（tracer/metrics 段驱动）

`configs/bald-admin.yaml` 的 `tracer` / `metrics` 段是远端 APM
（OTel Collector / VictoriaMetrics / Grafana Cloud）直推的**主配置渠道**——指标
（Prometheus 暴露端点 + OTLP 直推双通道）与 trace（OTLP 直推）一并驱动，核心埋点
（grpc/gin Observability 起 span、AuditWithMetrics emit）零改动：

```yaml
tracer:
  type: "otlp"                        # 段存在则 type 必填（空串启动期报错）
  otlp: { endpoint: "10.x.x.x:4318", insecure: true, sampler: "always_on" }
metrics:
  type: "otlp"                        # "prometheus"=仅本地抓取 / "otlp"=双通道
  prometheus: { addr: ":9091", path: "/metrics" }
  otlp: { endpoint: "10.x.x.x:4318", insecure: true, push_interval: 15 }
```

**显式主开关契约**（行为由 type 声明决定，不靠 endpoint 反推）：

- 段整体缺省 → 零配置默认：trace no-op + 仅 Prometheus `:9091` 本地抓取；
- 段存在 → `type` 必须显式声明：`tracer.type` 仅支持 `"otlp"`；`metrics.type`
  支持 `"prometheus"`（仅暴露）/ `"otlp"`（暴露+直推）——空串或未知值启动期报错；
- `type=otlp` 而 endpoint 空（含 env）→ 启动报错；`type=prometheus` 而 otlp
  endpoint（含 env）非空 → 矛盾配置启动报错。

裸 `host:port` 默认 insecure（内网 collector）；`insecure: false` 走 TLS；`http(s)://`
前缀按完整 URL 解析；`headers` 支持远端鉴权（如 Grafana Cloud Bearer token）。

env 覆盖通道保留（优先级 env > yaml，只提供地址、不改变 type 语义）：
`BALD_ADMIN_OTLP_ADDR` 覆盖双通道 endpoint、`BALD_ADMIN_METRICS_ADDR` 覆盖暴露
端口——Taskfile 冒烟与 CI 既有用法不破。

> 云端核对时注意 OTLP→Prometheus 的 `service.name`→`job` 标签映射：按
> `service_name` 查是假阴性，应按 `job="bald-admin"` 查。

## 验证接口

```bash
# 健康检查（公开）
curl -i http://127.0.0.1:8080/v1/ping
curl -i http://127.0.0.1:8080/v1/info

# 登录拿 token（公开）→ 访问受限接口
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | jq -r .token)
curl -i http://127.0.0.1:8080/v1/secret/secret-1 \
  -H "Authorization: Bearer $TOKEN"
curl -i http://127.0.0.1:8080/v1/secret/secret-1 \
  -X DELETE -H "Authorization: Bearer $TOKEN"
```

测试账号：`admin/admin123`(角色 admin)、`alice/alice123`(viewer)、`bob/bob`(t-other 租户)。
viewer 删机密 → 403；跨租户查 `bob` 的机密 → 404（租户隔离）。

## 测试

```bash
# 用 Taskfile（推荐，跨平台）
task test          # go test -shuffle=on ./...
task build         # go build ./...

# 或直接 go
go test -shuffle=on ./...
```

### 需要外部依赖的测试

部分 e2e 依赖真实外部服务。**未配置时自动 Skip**（打印「环境缺失，非验证失败」，
不会伪装通过）；要真跑需经环境变量注入地址：

| 依赖 | Task 目标 | 注入的 env |
|---|---|---|
| Redis（token/captcha/MFA/broker） | `task test:redis` | `BALD_E2E_REDIS_ADDR`（默认 `127.0.0.1:6379`）、`BALD_E2E_REDIS_PASSWORD` |
| MinIO（文件模块） | `task test:file` | `MINIO_TEST_ENDPOINT` 等 |

```bash
# 指向非本地 Redis（如集群内实例；受管实例通常强制鉴权，必须带密码）
task test:redis REDIS_TEST_ADDR=10.82.138.249:30967 REDIS_TEST_PASSWORD=***

# 或直接 go（env 名同左表）
BALD_E2E_REDIS_ADDR=10.82.138.249:30967 BALD_E2E_REDIS_PASSWORD=*** \
  go test ./internal/apiserver/e2e/ -run TestWave1d
```

Redis 测试按 **DB 12-15** 隔离（各测试用不同 DB，互不污染）。

## 目录

```
backend/                                (独立 go module)
├── appspec.json                    AppSpec 声明：12 个 proto 支持域的清单真相源
├── cmd/bald-admin/main.go          入口：appkit.Run + 拦截器链序 + metrics/audit/nacos 接线
├── cmd/bald-admin/app/             装配层：assembly/servers/providers/audit/后端子模块
├── configs/bald-admin.yaml.example 契约驱动配置模板（bconf proto 为唯一真相源）
├── api/
│   ├── protos/<域>/v1/*.proto      业务契约（buf 模块根）
│   ├── buf.*.yaml / buf.lock       buf 生成配置
│   └── gen/go/<域>/v1/*.pb.go      proto 生成物
├── internal/
│   ├── apiserver/
│   │   ├── biz/v1/<域>/            业务逻辑
│   │   ├── handler/gin/ + grpc/    协议接入层（HTTP 路由 / gRPC service）
│   │   ├── handler/convert/        proto ↔ 内部类型
│   │   ├── domains/                每域的装配声明（Domain 值 + Deps）
│   │   ├── model/                  gorm 模型
│   │   ├── assets/                 内嵌 openapi.yaml
│   │   └── e2e/                    端到端测试（真实 gin+store+拦截器链）
│   ├── bootstrap/                  InitBridges + Configure（PG/Redis/MinIO/策略装载）
│   ├── domainset/                  appspec.json 派生的域清单（生成物 + Declared 访问器）
│   └── security/{casbin,audit}/    授权（策略数据化）/审计后端桥接
└── docs/                           设计/需求文档
```
