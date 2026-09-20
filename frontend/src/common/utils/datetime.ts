import dayjs from "dayjs"

const INVALID_DATE = "N/A"

/** 格式化日期时间 */
export function formatDateTime(datetime: string | number | Date = "", template: string = "YYYY-MM-DD HH:mm:ss") {
  const day = dayjs(datetime)
  return day.isValid() ? day.format(template) : INVALID_DATE
}

/**
 * 格式化「秒级 Unix 时间戳」（int64，B 轨手写 gin 的 encoding/json 输出）。
 *
 * 后端手写 gin 面的时间字段是 `time.Time.Unix()`（秒），而 dayjs 默认按毫秒
 * 解析数字——直接传秒戳会得到 1970 年。此函数负责 ×1000 转换。
 *
 * 与 formatDateTime 的分工（Wave 7 视觉终验发现的单位混用缺陷）：
 *   - B 轨（手写 gin + c.JSON）：int64 秒 → 用本函数
 *   - A 轨（protojson Timestamp）：RFC3339 字符串 → 用 formatDateTime
 * 0 / undefined（后端 omitempty 缺省）视为无值，返回 N/A。
 */
export function formatUnixSeconds(seconds?: number, template: string = "YYYY-MM-DD HH:mm:ss") {
  if (!seconds) return INVALID_DATE
  return formatDateTime(seconds * 1000, template)
}
