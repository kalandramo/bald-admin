// Package loginpolicy 提供登录策略的**值格式校验**（Wave 1.6）。
//
// 为什么单独成包：源的 `ValidateLoginPolicyValue`（`login_policy_checker.go:147`）
// 承担一个关键安全职责——**白名单策略若配错格式，匹配器会静默不命中，导致全员
// 被锁**（源注释原话：「配错格式会让匹配器静默不命中（白名单 = 全员被锁）」）。
// 故 Create/Update 必须在落库前拒绝格式错误的值。
//
// 本包逐条对齐源的校验规则：
//   - IP：支持单个 IP 与 CIDR（含 "/" 时按 CIDR 解析）；
//   - TIME：必须是 `HH:MM-HH:MM`，且起止不相等；
//   - DEVICE/MAC：长度 ≤ 128；
//   - REGION：长度 ≤ 32；
//   - 未知 method：拒绝（源同此——宁可拒绝也不放行未识别的策略）。
package loginpolicy

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Method 常量（对齐源 proto 的 LoginPolicy.Method）。
const (
	MethodIP     = "IP"
	MethodMAC    = "MAC"
	MethodRegion = "REGION"
	MethodTime   = "TIME"
	MethodDevice = "DEVICE"
)

// Type 常量（对齐源 proto 的 LoginPolicy.Type）。
const (
	TypeBlacklist = "BLACKLIST"
	TypeWhitelist = "WHITELIST"
)

// ValidateValue 校验策略值格式（对齐源 ValidateLoginPolicyValue）。
// 返回 nil 表示格式合法。
func ValidateValue(method, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("policy value is empty")
	}
	switch strings.ToUpper(method) {
	case MethodIP:
		// 含 "/" 按 CIDR 解析，否则按单个 IP。
		if strings.Contains(value, "/") {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("invalid CIDR: %s", value)
			}
			return nil
		}
		if net.ParseIP(value) == nil {
			return fmt.Errorf("invalid IP: %s", value)
		}
		return nil
	case MethodTime:
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			return fmt.Errorf("time window must be HH:MM-HH:MM")
		}
		s1, ok1 := parseHHMM(parts[0])
		s2, ok2 := parseHHMM(parts[1])
		if !ok1 || !ok2 {
			return fmt.Errorf("invalid time format: %s", value)
		}
		if s1 == s2 {
			return fmt.Errorf("time window start equals end: %s", value)
		}
		return nil
	case MethodDevice, MethodMAC:
		if len(value) > 128 {
			return fmt.Errorf("value too long")
		}
		return nil
	case MethodRegion:
		if len(value) > 32 {
			return fmt.Errorf("region code too long")
		}
		return nil
	}
	// 未知 method：拒绝（源同此）。
	return fmt.Errorf("unknown policy method: %s", method)
}

// parseHHMM 解析 HH:MM 为当日秒数。格式非法返回 (0, false)。
func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*3600 + m*60, true
}

// ValidateType 校验策略类型（BLACKLIST / WHITELIST）。
func ValidateType(t string) error {
	switch strings.ToUpper(t) {
	case TypeBlacklist, TypeWhitelist:
		return nil
	}
	return fmt.Errorf("unknown policy type: %s", t)
}
