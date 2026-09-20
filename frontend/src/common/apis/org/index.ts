// 组织架构域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/org.go：
//   org_unit 7 条 + position 7 条，全部挂 /v1 分组（认证 + 授权）。
// 列表无分页参数（biz 全量返回），故不做分页参数透传。
import type { BatchCreateResponse, CountResponse, ListResponse, MutationResponse, OrgUnit, Position } from "./type"
import { request } from "@/http/axios"

// ---- org_unit ----

/** 创建组织单元（201，返回裸对象） */
export function createOrgUnitApi(data: OrgUnit) {
  return request<OrgUnit>({ url: "v1/org-units", method: "post", data })
}

/** 组织单元树（返回 {"items":[...]}，children 嵌套） */
export function listOrgUnitsApi() {
  return request<ListResponse<OrgUnit>>({ url: "v1/org-units", method: "get" })
}

/** 组织单元计数 */
export function countOrgUnitsApi() {
  return request<CountResponse>({ url: "v1/org-units/count", method: "get" })
}

/** 组织单元详情（按 code） */
export function getOrgUnitApi(code: string) {
  return request<OrgUnit>({ url: `v1/org-units/${code}`, method: "get" })
}

/** 更新组织单元（按 code） */
export function updateOrgUnitApi(code: string, data: OrgUnit) {
  return request<MutationResponse>({ url: `v1/org-units/${code}`, method: "put", data })
}

/** 删除组织单元（有子节点时报 400） */
export function deleteOrgUnitApi(code: string) {
  return request<MutationResponse>({ url: `v1/org-units/${code}`, method: "delete" })
}

/** 批量创建组织单元（部分成功语义） */
export function batchCreateOrgUnitsApi(items: OrgUnit[]) {
  return request<BatchCreateResponse<OrgUnit>>({ url: "v1/org-units/batch", method: "post", data: { items } })
}

// ---- position ----

/** 创建岗位（201，返回裸对象） */
export function createPositionApi(data: Position) {
  return request<Position>({ url: "v1/positions", method: "post", data })
}

/** 岗位列表（返回 {"items":[...]}） */
export function listPositionsApi() {
  return request<ListResponse<Position>>({ url: "v1/positions", method: "get" })
}

/** 岗位计数 */
export function countPositionsApi() {
  return request<CountResponse>({ url: "v1/positions/count", method: "get" })
}

/** 岗位详情（按 code） */
export function getPositionApi(code: string) {
  return request<Position>({ url: `v1/positions/${code}`, method: "get" })
}

/** 更新岗位（按 code） */
export function updatePositionApi(code: string, data: Position) {
  return request<MutationResponse>({ url: `v1/positions/${code}`, method: "put", data })
}

/** 删除岗位（按 code） */
export function deletePositionApi(code: string) {
  return request<MutationResponse>({ url: `v1/positions/${code}`, method: "delete" })
}

/** 批量创建岗位（部分成功语义） */
export function batchCreatePositionsApi(items: Position[]) {
  return request<BatchCreateResponse<Position>>({ url: "v1/positions/batch", method: "post", data: { items } })
}
