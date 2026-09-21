// 组织架构域类型（Wave P0.5：已迁移为 A 轨 proto 契约）。
//
// **类型来源改为 orval 生成的 model**（契约单一真相源：api/protos/identity/v1）。
// 字段名为 snake_case（protojson UseProtoNames 输出），与线上响应逐字段对齐。
//
// 此前手写 interface 已删除——契约变更时无需手工同步类型（生成即同步）。
export type { OrgUnit, Position } from "@/api/generated/model"

// 枚举常量（供页面做下拉/比对，值即 protojson 输出的符号名）。
export { OrgUnitStatus, OrgUnitType, PositionStatus, PositionType } from "@/api/generated/model"
