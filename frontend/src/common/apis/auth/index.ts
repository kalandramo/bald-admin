import type { CurrentUser, LoginResponse } from "@@/types/api"
import { request } from "@/http/axios"

/** 登录 */
export function loginApi(data: { username: string, password: string }) {
  return request<LoginResponse>({
    url: "v1/login",
    method: "post",
    data
  })
}

/** 获取当前用户信息 */
export function whoamiApi() {
  return request<CurrentUser>({
    url: "v1/auth/whoami",
    method: "get"
  })
}
