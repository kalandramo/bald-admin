// Package assets 内嵌服务静态资产。
//
// openapi.yaml 由 buf.openapi.gen.yaml 直出本目录（proto 注解单一真相源），
// go:embed 编译进二进制——文档与代码版本零漂移；该文件入库（embed 编译期
// 依赖，且 CI 无 buf 环境时也能构建）。
package assets

import _ "embed"

// OpenAPISpec 是 OpenAPI v3 契约（naming=proto 保 snake_case，与后端
// writePB 的 protojson UseProtoNames 输出逐字段对齐；enum_type=string
// 输出枚举名）。运行期经 GET /openapi.yaml 端点 serve；前端 orval 生成
// 也直接读本文件（服务内嵌与前端生成消费同一份产物）。
//
//go:embed openapi.yaml
var OpenAPISpec []byte
