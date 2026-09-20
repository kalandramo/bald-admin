// 身份凭证域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/identity/identity.go）。

/** 用户凭证（不含明文密码） */
export interface Credential {
  id: string
  user_id: string
  tenant_id: string
  identity_type: string
  identifier: string
  credential_type: string
  status: string
  created_at?: number
}

/** 登录策略 */
export interface LoginPolicy {
  id: string
  tenant_id: string
  target_id?: string
  type: string
  method: string
  value: string
  reason?: string
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

/** 凭证校验响应（恒 200，valid 为业务结果） */
export interface VerifyResponse {
  valid: boolean
  user_id: string
}
