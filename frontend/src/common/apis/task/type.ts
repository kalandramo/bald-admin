// 任务调度域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/task/task.go）。

/** 任务 */
export interface Task {
  id: string
  type_name: string
  type: string
  cron_spec?: string
  task_payload?: string
  enable: boolean
  task_options?: string
  remark?: string
  running: boolean
  created_at?: number
}

/** 列表响应 */
export interface ListResponse<T> {
  items: T[]
}

/** 计数响应 */
export interface CountResponse {
  total: number
}

/** 变更确认响应 */
export interface MutationResponse {
  message: string
}
