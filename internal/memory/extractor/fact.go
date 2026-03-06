package extractor

import "strings"

// extractFacts 从用户消息识别稳定事实。
func extractFacts(msg string) []FactMemory {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	out := make([]FactMemory, 0, 4)

	if v := extractOccupation(msg); v != "" {
		out = append(out, FactMemory{Key: "occupation", Value: v, Confidence: 0.82, Importance: 4})
	}
	if v := extractNickname(msg); v != "" {
		out = append(out, FactMemory{Key: "nickname", Value: v, Confidence: 0.9, Importance: 4})
	}
	if v := extractTimezone(msg); v != "" {
		out = append(out, FactMemory{Key: "timezone", Value: v, Confidence: 0.8, Importance: 3})
	}
	return out
}

func extractOccupation(msg string) string {
	keywords := []string{"工程师", "程序员", "老师", "设计师", "产品经理", "学生", "医生", "运营", "律师"}
	if !(strings.Contains(msg, "我是") || strings.Contains(msg, "我在") || strings.Contains(msg, "我做")) {
		return ""
	}
	for _, kw := range keywords {
		if strings.Contains(msg, kw) {
			return kw
		}
	}
	return ""
}

func extractNickname(msg string) string {
	marks := []string{"你可以叫我", "叫我", "我的昵称是"}
	for _, m := range marks {
		if strings.Contains(msg, m) {
			v := trimMemoryValue(afterFirst(msg, m))
			v = strings.Trim(v, "“”\"' ")
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func extractTimezone(msg string) string {
	pairs := []string{"北京时间", "上海时间", "纽约时间", "东京时间", "UTC+8", "UTC+9", "UTC-5"}
	for _, p := range pairs {
		if strings.Contains(strings.ToUpper(msg), strings.ToUpper(p)) {
			return p
		}
	}
	return ""
}

func dedupFacts(items []FactMemory) []FactMemory {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]FactMemory{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Key))
		if old, ok := seen[key]; ok {
			if item.Confidence > old.Confidence {
				seen[key] = item
			}
			continue
		}
		seen[key] = item
	}
	out := make([]FactMemory, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	return out
}

// extractFactsWithContext 处理 assistant 追问后的短回答场景（职业/昵称/时区）。
func extractFactsWithContext(msg, assistantMsg, conversationContext string) []FactMemory {
	msg = strings.TrimSpace(msg)
	assistantMsg = strings.TrimSpace(assistantMsg)
	conversationContext = strings.TrimSpace(conversationContext)
	if msg == "" || assistantMsg == "" {
		return nil
	}
	text := strings.ToLower(assistantMsg + " " + conversationContext)
	out := make([]FactMemory, 0, 2)

	if looksLikeOccupationQuestion(text) {
		if v := extractOccupationFromShort(msg); v != "" {
			out = append(out, FactMemory{Key: "occupation", Value: v, Confidence: 0.76, Importance: 4})
		}
	}
	if looksLikeNicknameQuestion(text) {
		v := trimMemoryValue(msg)
		v = strings.Trim(v, "“”\"' ")
		if v != "" && runeLen(v) <= 16 {
			out = append(out, FactMemory{Key: "nickname", Value: v, Confidence: 0.78, Importance: 4})
		}
	}
	if looksLikeTimezoneQuestion(text) {
		if v := extractTimezone(msg); v != "" {
			out = append(out, FactMemory{Key: "timezone", Value: v, Confidence: 0.74, Importance: 3})
		}
	}
	return out
}

func looksLikeOccupationQuestion(text string) bool {
	keywords := []string{"做什么工作", "职业", "你是做", "在哪上班", "什么岗位"}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func looksLikeNicknameQuestion(text string) bool {
	keywords := []string{"怎么称呼", "叫你什么", "你的昵称", "我该怎么叫你"}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func looksLikeTimezoneQuestion(text string) bool {
	keywords := []string{"你在哪个时区", "当地时间", "时区", "utc"}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func extractOccupationFromShort(msg string) string {
	keywords := []string{"工程师", "程序员", "老师", "设计师", "产品经理", "学生", "医生", "运营", "律师"}
	for _, kw := range keywords {
		if strings.Contains(msg, kw) {
			return kw
		}
	}
	return ""
}
