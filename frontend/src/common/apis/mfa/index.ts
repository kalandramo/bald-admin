// MFA 域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/mfa.go（11 条）。
// 注：challenge/start、backup-codes/generate、backup-codes（GET/POST）源未实现，
// 返回 501——前端不暴露入口，仅保留此说明。
import type { ConfirmResponse, ListResponse, MFAStatus, MutationResponse, StartEnrollResult } from "./type"
import { request } from "@/http/axios"

/** MFA 状态（当前用户） */
export function getMFAStatusApi() {
  return request<MFAStatus>({ url: "v1/mfa/status", method: "get" })
}

/** 已绑定方法列表 */
export function listMFAMethodsApi() {
  return request<ListResponse<{ id: string, method: string, display: string, enabled: boolean }>>({
    url: "v1/mfa/methods",
    method: "get"
  })
}

/** 开始绑定（返回 secret + otpauth URL，secret 仅此一次） */
export function startMFAEnrollApi() {
  return request<StartEnrollResult>({ url: "v1/mfa/enroll/start", method: "post" })
}

/** 确认绑定（校验 TOTP 码） */
export function confirmMFAEnrollApi(operationId: string, totpCode: string, display: string) {
  return request<ConfirmResponse>({
    url: "v1/mfa/enroll/confirm",
    method: "post",
    data: { operation_id: operationId, totp_code: totpCode, display }
  })
}

/** 禁用 MFA（user_id 可空=当前用户；管理端可指定他人） */
export function disableMFAApi(credentialId: string, userId?: string) {
  return request<MutationResponse>({
    url: "v1/mfa/disable",
    method: "post",
    data: { credential_id: credentialId, user_id: userId ?? "" }
  })
}

/** 撤销设备 */
export function revokeMFADeviceApi(credentialId: string, userId?: string) {
  return request<MutationResponse>({
    url: "v1/mfa/device/revoke",
    method: "post",
    data: { credential_id: credentialId, user_id: userId ?? "" }
  })
}

/** 登录挑战验证（公开端点，登录流程用） */
export function verifyMFAChallengeApi(operationId: string, totpCode: string) {
  return request<{ success: boolean }>({
    url: "v1/mfa/challenge/verify",
    method: "post",
    data: { operation_id: operationId, totp_code: totpCode }
  })
}
