// 组织架构域类型（手写 gin 面，无 proto 契约）。
// 字段名逐字对齐后端 biz json tag（internal/apiserver/biz/v1/org/org.go）——
// 后端 c.JSON 直接输出 Go struct，字段名即 json tag，不得自造。

/** 组织单元（树形，parent_id/children 构成层级） */
export interface OrgUnit {
  id: string
  name: string
  code: string
  type: string
  parent_id?: string
  path?: string
  status: string
  sort_order: number
  leader_id?: string
  leader_name?: string
  remark?: string
  description?: string
  children?: OrgUnit[]
}

/** 岗位（org_unit_id/org_unit_name 为关联回填） */
export interface Position {
  id: string
  name: string
  code: string
  headcount: number
  status: string
  type: string
  org_unit_id?: string
  org_unit_name?: string
  reports_to_position_id?: string
  reports_to_position_name?: string
  job_family?: string
  job_grade?: string
  level: number
  is_key_position: boolean
  remark?: string
}

/** 列表响应（handler 包成 {"items":[...]}） */
export interface ListResponse<T> {
  items: T[]
}

/** 计数响应（handler 包成 {"total":n}） */
export interface CountResponse {
  total: number
}

/** 变更确认响应（handler 包成 {"message":"..."}） */
export interface MutationResponse {
  message: string
}

/** 批量创建响应（部分成功语义，HTTP 恒 200） */
export interface BatchCreateResponse<T> {
  created: T[]
  failed: string[]
}
