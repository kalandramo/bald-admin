// 身份凭证域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/identity.go（15 条）：
//   credential 8 + login_policy 5 + profile 2（源空实现 → 501）。
import type { CountResponse, Credential, ListResponse, LoginPolicy, MutationResponse, VerifyResponse } from "./type"
import { request } from "@/http/axios"

// ---- user_credential ----

/** 创建凭证（201；password 明文，biz 层哈希） */
export function createCredentialApi(data: {
  user_id: string
  identity_type: string
  identifier: string
  credential_type: string
  password: string
}) {
  return request<Credential>({ url: "v1/credentials", method: "post", data })
}

/** 凭证列表（按 user_id 过滤） */
export function listCredentialsApi(userId: string) {
  return request<ListResponse<Credential>>({ url: "v1/credentials", method: "get", params: { user_id: userId } })
}

/** 凭证详情 */
export function getCredentialApi(id: string) {
  return request<Credential>({ url: `v1/credentials/${id}`, method: "get" })
}

/** 按标识查凭证 */
export function getCredentialByIdentifierApi(identityType: string, identifier: string) {
  return request<Credential>({
    url: "v1/credentials/by-identifier",
    method: "get",
    params: { identity_type: identityType, identifier }
  })
}

/** 删除凭证 */
export function deleteCredentialApi(id: string) {
  return request<MutationResponse>({ url: `v1/credentials/${id}`, method: "delete" })
}

/** 校验凭证（公开端点，恒 200） */
export function verifyCredentialApi(data: { identity_type: string, identifier: string, password: string }) {
  return request<VerifyResponse>({ url: "v1/credentials/verify", method: "post", data })
}

/** 修改凭证（需旧密码） */
export function changeCredentialApi(data: {
  identity_type: string
  identifier: string
  old_password: string
  new_password: string
}) {
  return request<MutationResponse>({ url: "v1/credentials/change", method: "post", data })
}

/** 重置凭证（不需旧密码，权限由 authz 限制） */
export function resetCredentialApi(data: { identity_type: string, identifier: string, new_password: string }) {
  return request<MutationResponse>({ url: "v1/credentials/reset", method: "post", data })
}

// ---- login_policy ----

/** 创建登录策略（201） */
export function createLoginPolicyApi(data: {
  target_id: string
  type: string
  method: string
  value: string
  reason: string
}) {
  return request<LoginPolicy>({ url: "v1/login-policies", method: "post", data })
}

/** 登录策略列表 */
export function listLoginPoliciesApi() {
  return request<ListResponse<LoginPolicy>>({ url: "v1/login-policies", method: "get" })
}

/** 登录策略计数 */
export function countLoginPoliciesApi() {
  return request<CountResponse>({ url: "v1/login-policies/count", method: "get" })
}

/** 登录策略详情 */
export function getLoginPolicyApi(id: string) {
  return request<LoginPolicy>({ url: `v1/login-policies/${id}`, method: "get" })
}

/** 删除登录策略 */
export function deleteLoginPolicyApi(id: string) {
  return request<MutationResponse>({ url: `v1/login-policies/${id}`, method: "delete" })
}
