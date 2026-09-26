// pb.go 统一 proto 消息的 HTTP 绑定与序列化（T2）。
//
// 为什么不用 c.ShouldBindJSON / c.JSON（encoding/json）：grpc-gateway 按 proto3 JSON
// 规范（protojson）编解码——枚举输出符号名（"FREEZE"）、Timestamp 输出 RFC3339、
// int64/uint64 输出字符串；encoding/json 会把枚举输出数字、Timestamp 输出内部字段、
// 且对 int64 输出裸数字。同一路由经 gateway（:8081）与直连 gin（:8080）会得到不同
// JSON——违反「REST 与 gRPC 同源」的 P9 语义。本文件强制两侧统一 protojson。
//
// 错误出口统一走框架 web.ErrorResponse（决策⑧）：错误体与 grpc-gateway 转码的
// google.rpc.Status JSON 结构同形（{"code","message","details":[{"@type",reason,
// domain,metadata}]}），三面（gin/gRPC/gateway）一份契约。
package gin

import (
	"errors"
	"io"

	gingonic "github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/kalandramo/bald/berrors"
	"github.com/kalandramo/bald/pkg/store"
	web "github.com/kalandramo/bald/transport/web"

	validation "github.com/kalandramo/bald-admin/internal/security/validation"
)

// bindPB 按 proto3 JSON 规范把请求体绑定到 proto 消息（DiscardUnknown 兼容宽松客户端）。
func bindPB(c *gingonic.Context, req proto.Message) error {
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(b, req)
}

// writePB 按 proto3 JSON 规范写出响应（与 grpc-gateway 转码结果一致）。
// UseProtoNames 输出 proto 字段原名（snake_case，如 created_at）：前端 types/api.ts
// 全部按 snake_case 建模，protojson 默认 camelCase（createdAt）会导致字段静默错位。
func writePB(c *gingonic.Context, code int, msg proto.Message) {
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		web.ErrorResponse(c, err)
		return
	}
	c.Data(code, "application/json", b)
}

// bindErr 统一绑定失败响应：INVALID_ARGUMENT（400）+ message=错误串、无 details——
// 与 grpc-gateway 对非法请求体的转码形状一致。bindPB 与 c.ShouldBindJSON 的失败
// 都走这里。
func bindErr(c *gingonic.Context, err error) {
	web.ErrorResponse(c, berrors.BadRequest("").WithMessage("%s", err))
}

// validatePB 对已绑定的 proto 消息执行 buf.validate 注解校验（Wave 校验层契约化）。
//
// 为什么 gin 面需要显式调用：gRPC 面经 ValidatorInterceptor 统一拦截，而 gin
// 直连路径不经过 gRPC 拦截器链——两侧必须各自触发同一份注解规则（规则本身
// 只有一份，在 proto 契约里）。gateway 转码面走 gRPC，天然被拦截器覆盖。
//
// 返回 true 表示校验通过；false 表示已写出错误响应（调用方直接 return）。
// 与 bindErr 同形：错误经 web.ErrorResponse 出口，保证三面错误体结构一致。
//
// 校验器为包级单例（protovalidate 实例预热 CEL 编译，构造一次复用）。
func validatePB(c *gingonic.Context, msg proto.Message) bool {
	if err := pbValidator.Validate(c.Request.Context(), msg); err != nil {
		web.ErrorResponse(c, err)
		return false
	}
	return true
}

// pbValidator 是 gin 面共享的校验器单例（懒初始化，构造失败时 panic —— 注解
// 写错应当在首次使用时立即暴露，而非静默放行全部请求）。
var pbValidator = validation.MustNew()

// writeBizErr 统一 biz 错误 → 决策⑧错误体（T2 起的三层判定语义保持）：
//   - *berrors.Error → 框架 ErrorResponse 主路径（httperr.CodeToHTTP 映射，
//     message=可展示文案，reason/metadata 进 details）；
//   - store.ErrNotFound / ErrConflict 哨兵（biz 经 fmt.Errorf %w 包装，errors.Is
//     可穿透多层）→ NotFound / AlreadyExists 构造后走框架出口（cause 保留全链）；
//   - 其余 → 框架兜底 INTERNAL（500）——内部错误不得吞成 4xx，误导排障。
func writeBizErr(c *gingonic.Context, err error) {
	if _, ok := berrors.FromError(err); !ok {
		switch {
		case errors.Is(err, store.ErrNotFound):
			err = berrors.NotFound("not_found").WithCause(err)
		case errors.Is(err, store.ErrConflict):
			err = berrors.AlreadyExists("already_exists").WithCause(err)
		case errors.Is(err, store.ErrInvalidToken):
			// 分页游标非法（框架 tokenPaginator 对非 base64/非十进制 token 返回
			// 此哨兵）→ 400。不映射会落兜底 500，把客户端错误报成服务端错误。
			err = berrors.BadRequest("invalid_page_token").WithCause(err)
		}
	}
	web.ErrorResponse(c, err)
}
