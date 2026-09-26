// Package validation 提供基于 protovalidate 的请求校验（Wave 校验层契约化）。
//
// 设计意图（对齐《架构改进路线图》P6 的分层校验）：
//   - **声明式字段规则**（非空/长度/格式）写在 proto 的 buf.validate 注解里，
//     由 protovalidate 运行时读取执行——规则与契约同源，gRPC/HTTP 多面共用一份；
//   - **命令式复杂逻辑**（查库、权限、跨字段业务不变量）仍归 biz 层——注解表达
//     不了，框架 pkg/validation 的按类型分发器可承载。
//
// 本包只做「注解规则」这一层的包装与错误映射，不承载业务规则。
//
// 为什么需要包装（而非直接用 protovalidate）：
//   - **错误映射**：protovalidate 返回 *protovalidate.ValidationError，需转成
//     框架的 *berrors.Error，才能被 gRPC 的 ErrorInterceptor / gin 的 writeBizErr
//     统一收口为 400（否则会落兜底 500，把客户端错误报成服务端错误）；
//   - **聚合展示**：protovalidate 默认累积全部违规（非 fail-fast），本包把
//     violations 聚合成一条可读 message（与 conf.Validate 收集全部问题的风格一致）。
package validation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"github.com/kalandramo/bald/berrors"
)

// Validator 包装 protovalidate 实例，提供框架风格的错误出口。
type Validator struct {
	pv protovalidate.Validator
}

// New 构造并预热校验器。
//
// 用 protovalidate.New() 而非每次调 protovalidate.Validate(msg)：New() 会预编译
// 注解中的 CEL 表达式，之后每次校验复用编译结果（生产推荐做法）。构造期即暴露
// 注解编译错误（fail fast），而非等到第一个请求进来才炸。
func New() (*Validator, error) {
	pv, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("validation: build protovalidate: %w", err)
	}
	return &Validator{pv: pv}, nil
}

// MustNew 同 New，但构造失败时 panic（供 main 装配期使用——注解写错应当启动即失败）。
func MustNew() *Validator {
	v, err := New()
	if err != nil {
		panic(err)
	}
	return v
}

// Validate 校验 msg 的 buf.validate 注解规则。
//
// 返回：
//   - nil：通过（**包括无任何注解的消息**——渐进推进的安全前提，未标注的
//     消息不受影响）；
//   - *berrors.Error（CodeInvalidArgument）：有违规，message 为聚合后的可读文案；
//   - 其他 error：非校验类错误（如注解编译错误）原样透传，**不吞**——避免把
//     框架级故障伪装成参数问题。
//
// 非 proto.Message 入参直接放行（gRPC 拦截器会对所有请求调用本方法，需容错）。
func (v *Validator) Validate(ctx context.Context, msg any) error {
	_ = ctx // protovalidate 不消费 ctx；保留签名以适配 grpcmw.MessageValidator。
	m, ok := msg.(proto.Message)
	if !ok {
		return nil
	}
	err := v.pv.Validate(m)
	if err == nil {
		return nil
	}
	return mapValidationError(err)
}

// mapValidationError 把 protovalidate 违规映射为框架错误模型。
//
// protovalidate 默认**累积全部违规**（非 fail-fast），这里把所有 violation 聚合
// 成一条消息——与框架 conf.Validate 收集全部问题的风格一致，便于调用方一次看全。
// 非 ValidationError（如注解编译错误 CompilationError）原样透传，不做吞掉。
func mapValidationError(err error) error {
	var valErr *protovalidate.ValidationError
	if !errors.As(err, &valErr) {
		return err
	}
	details := make([]string, 0, len(valErr.Violations))
	for _, v := range valErr.Violations {
		field := "<unknown>"
		if v.FieldDescriptor != nil {
			field = string(v.FieldDescriptor.Name())
		}
		details = append(details, fmt.Sprintf("%s: %s", field, v.Proto.GetMessage()))
	}
	// 注意：WithMessage 本身接受格式串（format string, args ...any），
	// 直接传参即可，不必先 fmt.Sprintf 再传入（后者会触发 vet 的
	// non-constant format string 告警）。
	return berrors.BadRequest("INVALID_ARGUMENT").
		WithMessage("请求参数校验失败: %s", strings.Join(details, "; "))
}

// 编译期契约：*Validator 的 Validate 方法满足 grpcmw.MessageValidator 签名
// （func(ctx, rq any) error）——gRPC 侧可直接用 v.Validate 作拦截器回调。
var _ = func() bool {
	var v *Validator
	var _ func(context.Context, any) error = v.Validate
	return true
}
