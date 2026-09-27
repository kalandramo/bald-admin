// Package convert 收敛 handler 层的「模型 / biz DTO → proto」转换函数。
//
// 为什么需要它：这些转换在 handler/gin 与 handler/grpc 两侧**各写过一份**，
// 长期靠命名后缀区分（toOrgUnitPB vs toOrgUnitPBGRPC）。第一手比对（2026-09-27，
// 逐函数 diff 归一化后缀后）确认它们**逐字同构**——属纯重复，不是「两端语义
// 不同所以各写各的」。本包把它们收敛为一处，两侧共同引用。
//
// 与 bald-admin 分层约定的关系：
//   - biz 层仍持自有 DTO（backend/docs/go-wind-admin 业务移植计划.md:156
//     「biz/v1/<mod>/（纯 Go 出入参，不依赖传输层）」）——本包不改变这一点；
//   - 本包只把「DTO → proto」这段表达集中，属 handler 层内部的整理。
//
// 依赖边界（重要）：本包**只** import model、各 biz DTO 包、api/gen/go/*。
// **不得 import handler/gin 或 handler/grpc**——否则形成循环依赖。
package convert
