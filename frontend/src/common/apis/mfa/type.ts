// MFA 域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/mfa/mfa.go）。

/** 已绑定的 MFA 方法 */
export interface EnrolledMethod {
  id: string
  method: string
  display: string
  enabled: boolean
  created_at?: number
  last_used_at?: number
}

/** MFA 状态（enforcement: NOT_REQUIRED / OPTIONAL / REQUIRED） */
export interface MFAStatus {
  enabled: boolean
  enforcement: string
  enrolled?: EnrolledMethod[]
}

/** 开始绑定的结果（secret 仅此一次返回） */
export interface StartEnrollResult {
  operation_id: string
  secret: string
  otp_auth_url: string
  expires_at: number
}

/** 列表响应 */
export interface ListResponse<T> {
  items: T[]
}

/** 绑定确认响应 */
export interface ConfirmResponse {
  success: boolean
  credential_id: string
}

/** 变更确认响应 */
export interface MutationResponse {
  message: string
}
