import { formatDateTime, formatUnixSeconds } from "@@/utils/datetime"
import { describe, expect, it } from "vitest"

// 回归保护：Wave 7 视觉终验发现的单位混用缺陷。
// 后端两种时间戳格式并存：
//   - B 轨（手写 gin + encoding/json）：int64 秒级 Unix（如 mfa/message/identity）
//   - A 轨（protojson Timestamp）：RFC3339 字符串（如 audit/cache-monitor）
// 前者必须 ×1000 才是毫秒，后者直接可解析。混用会分别显示 1970 / N/A。

describe("formatUnixSeconds", () => {
  it("秒级时间戳转正确日期（修复前显示 1970）", () => {
    // 1758000000 秒 = 2025-09-16（任何时区都是该日）
    const out = formatUnixSeconds(1758000000)
    expect(out).not.toContain("1970")
    expect(out).toContain("2025-09-16")
  })

  it("0 与 undefined 返回 N/A（后端 omitempty 缺省）", () => {
    expect(formatUnixSeconds(0)).toBe("N/A")
    expect(formatUnixSeconds(undefined)).toBe("N/A")
  })

  it("支持自定义模板", () => {
    expect(formatUnixSeconds(1758000000, "YYYY/MM/DD")).toBe("2025/09/16")
  })
})

describe("formatDateTime（A 轨 RFC3339 字符串）", () => {
  it("protojson Timestamp 字符串直接可解析（不需 ×1000）", () => {
    const out = formatDateTime("2025-09-20T00:40:00Z")
    expect(out).toContain("2025-09-20")
  })
})
