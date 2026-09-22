// 组织架构域 API（Wave P0.5：已迁移为 A 轨 proto 契约）。
//
// **本文件保留为薄 re-export 层**——函数名与旧手写版一致，便于既有页面渐进迁移；
// 实现全部来自 orval 生成的 client（契约单一真相源：api/protos/identity/v1）。
// 新代码请直接 import `@/api/generated/org-unit-service` / `position-service`。
//
// 响应形状随契约变化（对照旧版）：
//   - 列表：`{items}` → `{items, total}`
//   - 单对象：裸对象 → `{org_unit}` / `{position}` 包装
//   - 变更：`{message:"updated"}` → 返回更新后的对象
import {
  orgUnitServiceBatchCreateOrgUnits,
  orgUnitServiceCountOrgUnits,
  orgUnitServiceCreateOrgUnit,
  orgUnitServiceDeleteOrgUnit,
  orgUnitServiceGetOrgUnit,
  orgUnitServiceListOrgUnitChildren,
  orgUnitServiceListOrgUnits,
  orgUnitServiceUpdateOrgUnit,
} from "@/api/generated/org-unit-service"
import {
  positionServiceBatchCreatePositions,
  positionServiceCountPositions,
  positionServiceCreatePosition,
  positionServiceDeletePosition,
  positionServiceGetPosition,
  positionServiceListPositions,
  positionServiceUpdatePosition,
} from "@/api/generated/position-service"

export type {
  CreateOrgUnitRequest,
  CreatePositionRequest,
  OrgUnit,
  Position,
  UpdateOrgUnitRequest,
  UpdatePositionRequest,
} from "@/api/generated/model"

// ---- org_unit ----

export const createOrgUnitApi = orgUnitServiceCreateOrgUnit
export const listOrgUnitsApi = orgUnitServiceListOrgUnits
// listOrgUnitChildrenApi 懒加载某节点的直接子节点（树形展开用，见 el-table lazy）。
export const listOrgUnitChildrenApi = orgUnitServiceListOrgUnitChildren
export const countOrgUnitsApi = orgUnitServiceCountOrgUnits
export const getOrgUnitApi = orgUnitServiceGetOrgUnit
export const updateOrgUnitApi = orgUnitServiceUpdateOrgUnit
export const deleteOrgUnitApi = orgUnitServiceDeleteOrgUnit
export const batchCreateOrgUnitsApi = orgUnitServiceBatchCreateOrgUnits

// ---- position ----

export const createPositionApi = positionServiceCreatePosition
export const listPositionsApi = positionServiceListPositions
export const countPositionsApi = positionServiceCountPositions
export const getPositionApi = positionServiceGetPosition
export const updatePositionApi = positionServiceUpdatePosition
export const deletePositionApi = positionServiceDeletePosition
export const batchCreatePositionsApi = positionServiceBatchCreatePositions
