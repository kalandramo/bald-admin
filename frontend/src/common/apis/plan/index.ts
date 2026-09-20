// 套餐配额域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/plan.go（14 条）：
//   Plan 5 + PlanModule 5 + PlanQuota 4（源无 Get）。
import type { CreatePlanRequest, ListResponse, MutationResponse, Plan, PlanModule, PlanQuota } from "./type"
import { request } from "@/http/axios"

// ---- Plan ----

/** 创建套餐（201；code 与 Plan 字段平铺） */
export function createPlanApi(data: CreatePlanRequest) {
  return request<Plan>({ url: "v1/plans", method: "post", data })
}

/** 套餐列表 */
export function listPlansApi() {
  return request<ListResponse<Plan>>({ url: "v1/plans", method: "get" })
}

/** 套餐详情 */
export function getPlanApi(id: string) {
  return request<Plan>({ url: `v1/plans/${id}`, method: "get" })
}

/** 更新套餐 */
export function updatePlanApi(id: string, data: Plan) {
  return request<MutationResponse>({ url: `v1/plans/${id}`, method: "put", data })
}

/** 删除套餐（级联删其 modules/quotas） */
export function deletePlanApi(id: string) {
  return request<MutationResponse>({ url: `v1/plans/${id}`, method: "delete" })
}

// ---- PlanModule ----

/** 新增套餐模块（201） */
export function createPlanModuleApi(planId: string, module: string) {
  return request<PlanModule>({ url: "v1/plan-modules", method: "post", data: { plan_id: planId, module } })
}

/** 套餐模块列表（按 plan_id 过滤） */
export function listPlanModulesApi(planId: string) {
  return request<ListResponse<PlanModule>>({ url: "v1/plan-modules", method: "get", params: { plan_id: planId } })
}

/** 更新套餐模块 */
export function updatePlanModuleApi(id: string, module: string) {
  return request<MutationResponse>({ url: `v1/plan-modules/${id}`, method: "put", data: { module } })
}

/** 删除套餐模块 */
export function deletePlanModuleApi(id: string) {
  return request<MutationResponse>({ url: `v1/plan-modules/${id}`, method: "delete" })
}

// ---- PlanQuota（源无 Get）----

/** 新增套餐配额（201） */
export function createPlanQuotaApi(planId: string, quotaType: string, quotaValue: number) {
  return request<PlanQuota>({
    url: "v1/plan-quotas",
    method: "post",
    data: { plan_id: planId, quota_type: quotaType, quota_value: quotaValue }
  })
}

/** 套餐配额列表（按 plan_id 过滤） */
export function listPlanQuotasApi(planId: string) {
  return request<ListResponse<PlanQuota>>({ url: "v1/plan-quotas", method: "get", params: { plan_id: planId } })
}

/** 更新套餐配额值 */
export function updatePlanQuotaApi(id: string, quotaValue: number) {
  return request<MutationResponse>({ url: `v1/plan-quotas/${id}`, method: "put", data: { quota_value: quotaValue } })
}

/** 删除套餐配额 */
export function deletePlanQuotaApi(id: string) {
  return request<MutationResponse>({ url: `v1/plan-quotas/${id}`, method: "delete" })
}
