import type { AxiosError, AxiosInstance, AxiosRequestConfig } from "axios"
import type { ApiErrorBody } from "@/common/types/api"
import { getToken } from "@@/utils/local-storage"
import axios from "axios"
import { get, merge } from "lodash-es"
import { useUserStore } from "@/pinia/stores/user"

/** 拦截器 reject 出的错误：AxiosError + 后端 reason（决策⑧程序化出口） */
export type ApiError = AxiosError & { reason?: string }

function createInstance() {
  const instance = axios.create()
  instance.interceptors.request.use(
    config => config,
    error => Promise.reject(error)
  )
  instance.interceptors.response.use(
    (response) => {
      const responseType = response.config.responseType
      if (responseType === "blob" || responseType === "arraybuffer") return response.data
      return response.data
    },
    (error: ApiError) => {
      const status = get(error, "response.status")
      // 决策⑧：统一错误响应体 {"code","message","details":[{reason,...}]}，
      // gin/gateway 同形。展示读 message；程序化读 details[0].reason——
      // 挂到 error 上供下游按稳定标识分支（如区分会话过期与权限不足）。
      const errorData = get(error, "response.data") as ApiErrorBody | undefined
      const backendMessage = errorData?.message
      error.reason = errorData?.details?.[0]?.reason
      switch (status) {
        case 400:
          error.message = backendMessage || "请求错误"
          break
        case 401:
          error.message = backendMessage || "未授权"
          useUserStore().logout()
          break
        case 403:
          error.message = backendMessage || "拒绝访问"
          break
        case 404:
          error.message = backendMessage || "请求地址出错"
          break
        case 408:
          error.message = "请求超时"
          break
        case 500:
          error.message = backendMessage || "服务器内部错误"
          break
        case 501:
          error.message = "服务未实现"
          break
        case 502:
          error.message = "网关错误"
          break
        case 503:
          error.message = "服务不可用"
          break
        case 504:
          error.message = "网关超时"
          break
        case 505:
          error.message = "HTTP 版本不受支持"
          break
        default:
          error.message = backendMessage || error.message || "未知错误"
      }
      ElMessage.error(error.message)
      return Promise.reject(error)
    }
  )
  return instance
}

function createRequest(instance: AxiosInstance) {
  return <T>(config: AxiosRequestConfig): Promise<T> => {
    const token = getToken()
    const defaultConfig: AxiosRequestConfig = {
      // baseURL 必须是绝对根路径：无 .env 时 VITE_BASE_URL 为 undefined，axios 会用
      // 相对 URL 发请求——嵌套路由页面（如 /users/index）下 "v1/user" 被浏览器解析为
      // "/users/v1/user"，打不中 vite proxy 的 "/v1" 规则，SPA fallback 返回 HTML，
      // 响应解析静默得到 undefined 字段（列表显示"暂无数据"且无任何报错）。
      baseURL: import.meta.env.VITE_BASE_URL || "/",
      headers: {
        "Authorization": token ? (`Bearer ${token}`) : undefined,
        "Content-Type": "application/json"
      },
      data: {},
      timeout: 5000,
      withCredentials: false
    }
    const mergeConfig = merge(defaultConfig, config)
    return instance(mergeConfig)
  }
}

const instance = createInstance()

export const request = createRequest(instance)
