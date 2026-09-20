// 站内消息域 API（手写 gin 面，无 proto 契约）。
// 路由与形状对齐 internal/apiserver/handler/gin/message.go（21 条）：
//   message 7 + category 6 + inbox 5 + recipients 2 + send/revoke。
import type {
  Category,
  CountResponse,
  ListResponse,
  Message,
  MutationResponse,
  Recipient,
  SendResult
} from "./type"
import { request } from "@/http/axios"

// ---- message ----

/** 创建消息（201） */
export function createMessageApi(data: Partial<Message>) {
  return request<Message>({ url: "v1/messages", method: "post", data })
}

/** 消息列表 */
export function listMessagesApi() {
  return request<ListResponse<Message>>({ url: "v1/messages", method: "get" })
}

/** 发送消息（三模式：target_all / recipient_user_id / target_user_ids） */
export function sendMessageApi(data: {
  title: string
  content: string
  type: string
  category_id: string
  target_all: boolean
  recipient_user_id: string
  target_user_ids: string[]
}) {
  return request<SendResult>({ url: "v1/messages/send", method: "post", data })
}

/** 撤销消息 */
export function revokeMessageApi(id: string, userId?: string) {
  return request<MutationResponse>({
    url: `v1/messages/${id}/revoke`,
    method: "post",
    data: { user_id: userId ?? "" }
  })
}

/** 消息详情 */
export function getMessageApi(id: string) {
  return request<Message>({ url: `v1/messages/${id}`, method: "get" })
}

/** 更新消息 */
export function updateMessageApi(id: string, data: Partial<Message>) {
  return request<MutationResponse>({ url: `v1/messages/${id}`, method: "put", data })
}

/** 删除消息 */
export function deleteMessageApi(id: string) {
  return request<MutationResponse>({ url: `v1/messages/${id}`, method: "delete" })
}

// ---- category ----

/** 创建分类（201） */
export function createMessageCategoryApi(data: Partial<Category>) {
  return request<Category>({ url: "v1/message-categories", method: "post", data })
}

/** 分类列表 */
export function listMessageCategoriesApi() {
  return request<ListResponse<Category>>({ url: "v1/message-categories", method: "get" })
}

/** 分类计数 */
export function countMessageCategoriesApi() {
  return request<CountResponse>({ url: "v1/message-categories/count", method: "get" })
}

/** 更新分类 */
export function updateMessageCategoryApi(id: string, data: Partial<Category>) {
  return request<MutationResponse>({ url: `v1/message-categories/${id}`, method: "put", data })
}

/** 删除分类 */
export function deleteMessageCategoryApi(id: string) {
  return request<MutationResponse>({ url: `v1/message-categories/${id}`, method: "delete" })
}

// ---- 收件箱（任何登录用户操作自己的）----

/** 我的收件箱列表 */
export function listInboxApi() {
  return request<ListResponse<Recipient>>({ url: "v1/inbox", method: "get" })
}

/** 标记单条已读 */
export function markInboxReadApi(id: string) {
  return request<MutationResponse>({ url: `v1/inbox/${id}/read`, method: "post" })
}

/** 批量标记状态 */
export function markInboxStatusApi(ids: string[], status: string) {
  return request<MutationResponse>({ url: "v1/inbox/mark-status", method: "post", data: { ids, status } })
}

/** 从收件箱删除 */
export function deleteInboxApi(id: string) {
  return request<MutationResponse>({ url: `v1/inbox/${id}`, method: "delete" })
}

// ---- recipients ----

/** 收件人计数 */
export function countRecipientsApi() {
  return request<CountResponse>({ url: "v1/recipients/count", method: "get" })
}

/** 按 id 批量查收件人 */
export function getRecipientsByIdsApi(ids: string[]) {
  return request<ListResponse<Recipient>>({ url: "v1/recipients/by-ids", method: "post", data: { ids } })
}
