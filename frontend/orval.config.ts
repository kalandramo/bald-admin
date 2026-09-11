// orval：从 backend 内嵌资产 openapi.yaml（proto 注解单一真相源，OpenAPI v3，
// 与服务二进制同源的 spec——见 GET /openapi.yaml 端点）生成前端 TS client +
// 类型。改契约的流程：cd backend/api && buf generate --template
// buf.openapi.gen.yaml && cd ../../frontend && pnpm api:gen。
// 生成物禁止手改（clean: true 随时全量重生成）。
import { defineConfig } from "orval"

export default defineConfig({
  goBaldAdmin: {
    input: "../backend/internal/apiserver/assets/openapi.yaml",
    output: {
      target: "./src/api/generated/client.ts",
      schemas: "./src/api/generated/model",
      client: "axios-functions",
      mock: false,
      clean: true,
      // mode tags：按 swagger tag（= proto service 名）分文件，对应原手写层的按域分文件
      mode: "tags",
      override: {
        // transport 封装保留（契约同步设计 §3）：token 注入、错误拦截
        // （决策⑧ message/reason 出口）、baseURL 约束都在 request 里，
        // 生成物只负责 URL/method/类型。
        mutator: {
          path: "./src/http/axios.ts",
          name: "request"
        }
      }
    }
  }
})
