package music

import (
	"encoding/json"
	"strconv"
	"strings"
)

// 各音源返回的都是 map[string]any(JSON 解出),字段名各家不同且类型不稳定。
// 这里提供统一、可预测的取值逻辑,避免各包各写一份、行为还不一致。

// Str 按 key 优先级取第一个非空值;数字统一十进制格式化(不会出现 1e+06 这类科学计数法)
func Str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				return x
			}
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case json.Number:
			return x.String()
		case int:
			return strconv.Itoa(x)
		case int64:
			return strconv.FormatInt(x, 10)
		}
	}
	return ""
}

// Int 按 key 优先级取第一个可解析为整数的值
func Int(m map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			return int(x)
		case json.Number:
			if n, err := x.Int64(); err == nil {
				return int(n)
			}
		case int:
			return x
		case int64:
			return int(x)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
				return n
			}
		}
	}
	return 0
}

// Itoa 十进制整数转字符串
func Itoa(n int) string { return strconv.Itoa(n) }

// HasMore 统一分页判定:上游给了 total 就用 total,没给则退化为"本页是否满页"
func HasMore(page, limit, total, got int) bool {
	if total > 0 && limit > 0 {
		return page*limit < total
	}
	return limit > 0 && got >= limit
}
