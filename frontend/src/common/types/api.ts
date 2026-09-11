// 未契约化端点的裸响应类型（不经过 ApiResponseData 包装）。
// 业务域类型（user/tenant/menu/permission/dict/audit/file 等）已由
// src/api/generated/model（orval 从 OpenAPI 生成）承接，勿在此重复建模。

/** 登录响应（POST /v1/login，gin 特例端点，无 proto 契约） */
export interface LoginResponse {
  access_token: string
  expires_at: string
}

/** 当前用户信息（GET /v1/auth/whoami，gin 特例端点，无 proto 契约） */
export interface CurrentUser {
  username: string
  user_id: string
  tenant_id: string
  roles: string[]
  token_type: string
}

/** 文件上传响应（gin 面 multipart 特例端点，响应形如 { file: FileItem }） */
export interface FileItem {
  id: string
  provider: string
  bucket_name: string
  save_file_name: string
  file_directory: string
  file_name: string
  extension: string
  content_hash: string
  size: number
  link_url: string
  mime_type: string
  created_by: string
  created_at: string
  updated_at: string
}

/** 统一错误响应体（决策⑧：google.rpc.Status JSON，gin/gateway 同形） */
export interface ApiErrorBody {
  code: number
  message: string
  details: ApiErrorDetail[]
}

/** 错误详情（对齐 errdetails.ErrorInfo 的 protojson 形状；metadata 为 i18n 动态变量） */
export interface ApiErrorDetail {
  "@type": string
  "reason": string
  "domain": string
  "metadata": Record<string, string>
}
