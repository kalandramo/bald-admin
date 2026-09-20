// 任务调度域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/task.go（11 条）。
import type { CountResponse, ListResponse, MutationResponse, Task } from "./type"
import { request } from "@/http/axios"

/** 创建任务（201） */
export function createTaskApi(data: Partial<Task>) {
  return request<Task>({ url: "v1/tasks", method: "post", data })
}

/** 任务列表 */
export function listTasksApi() {
  return request<ListResponse<Task>>({ url: "v1/tasks", method: "get" })
}

/** 任务计数 */
export function countTasksApi() {
  return request<CountResponse>({ url: "v1/tasks/count", method: "get" })
}

/** 已注册的处理器类型名 */
export function listTaskTypesApi() {
  return request<{ type_names: string[] }>({ url: "v1/tasks/types", method: "get" })
}

/** 全部启动 */
export function startAllTasksApi() {
  return request<{ started: number }>({ url: "v1/tasks/start-all", method: "post" })
}

/** 全部停止 */
export function stopAllTasksApi() {
  return request<MutationResponse>({ url: "v1/tasks/stop-all", method: "post" })
}

/** 全部重启 */
export function restartAllTasksApi() {
  return request<{ restarted: number }>({ url: "v1/tasks/restart-all", method: "post" })
}

/** 任务详情（按 type_name） */
export function getTaskApi(typeName: string) {
  return request<Task>({ url: `v1/tasks/${typeName}`, method: "get" })
}

/** 更新任务 */
export function updateTaskApi(typeName: string, data: Partial<Task>) {
  return request<MutationResponse>({ url: `v1/tasks/${typeName}`, method: "put", data })
}

/** 删除任务 */
export function deleteTaskApi(typeName: string) {
  return request<MutationResponse>({ url: `v1/tasks/${typeName}`, method: "delete" })
}

/** 单任务启停 */
export function controlTaskApi(typeName: string, start: boolean) {
  return request<MutationResponse>({ url: `v1/tasks/${typeName}/control`, method: "post", data: { start } })
}
