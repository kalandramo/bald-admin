// 站内消息域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/message/message.go）。

/** 消息 */
export interface Message {
  id: string
  title: string
  content: string
  status: string
  type: string
  sender_id?: string
  sender_name?: string
  category_id?: string
  created_at?: number
}

/** 发送结果 */
export interface SendResult {
  message_id: string
  delivered: number
  failed: number
  broadcast: boolean
}

/** 消息分类 */
export interface Category {
  id: string
  name: string
  code: string
  sort_order: number
  enabled: boolean
  remark?: string
}

/** 收件人（收件箱条目） */
export interface Recipient {
  id: string
  message_id: string
  recipient_user_id: string
  sender_user_id?: string
  title: string
  content: string
  status: string
  read_at?: number
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
