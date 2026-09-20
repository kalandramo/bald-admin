// 套餐配额域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/plan/plan.go）。

/** 套餐 */
export interface Plan {
  id: string
  name: string
  version?: string
  expiry_policy?: string
  data_retention_days?: number
  status: string
  remark?: string
}

/** 套餐模块 */
export interface PlanModule {
  id: string
  plan_id: string
  module: string
}

/** 套餐配额 */
export interface PlanQuota {
  id: string
  plan_id: string
  quota_type: string
  quota_value: number
}

/** 创建套餐请求：code 与 Plan 字段同层平铺（handler 内嵌 struct） */
export interface CreatePlanRequest extends Omit<Plan, "id"> {
  code: string
}

/** 列表响应 */
export interface ListResponse<T> {
  items: T[]
}

/** 变更确认响应 */
export interface MutationResponse {
  message: string
}
