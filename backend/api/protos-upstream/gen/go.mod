// protos-upstream/gen 是 **独立 Go module**，与 bald-admin/backend 隔离。
//
// 为什么（D18 修复的配套）：
//   A 层已把 protos-upstream 做成独立 **buf** module（有自己的 buf.yaml），
//   但它仍属 bald-admin/backend 这个 **Go** module——于是 `go build ./...`
//   会扫到这里的 10 个包，把它们的外部依赖（gnostic / tx7do）拖进生产
//   go.mod，并显著拖慢构建（实测超时 >120s）。
//
//   补上独立 Go module 后，`./...` 不跨 module 边界，生产构建图恢复干净。
//   这是「探测产物与生产隔离」原则的完整落地——buf 与 Go 两层边界一致。
//
// 用途：仅供 A/B/C 三层能力探测的生成物编译验证，**不参与生产构建**。
module github.com/kalandramo/bald-admin/api/protos-upstream/gen

go 1.27.1

require (
	github.com/google/gnostic v0.7.1
	github.com/tx7do/go-crud/api v0.0.7
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/google/gnostic-models v0.7.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)
