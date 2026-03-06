package extractor

import "strings"

// extractPreferences 从用户消息识别偏好信息。
func extractPreferences(msg string) []PreferenceMemory {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return nil
	}

	prefixes := []string{
		"我喜欢", "我最喜欢", "我特别喜欢", "我爱", "我超爱", "我迷上了",
	}

	for _, p := range prefixes {
		if strings.Contains(msg, p) {
			value := strings.TrimSpace(afterFirst(msg, p))
			if value == "" {
				continue
			}
			value = trimMemoryValue(value)
			if value == "" {
				continue
			}
			return []PreferenceMemory{{
				Category:   inferPreferenceCategory(value),
				Value:      value,
				Confidence: 0.82,
				Importance: 3,
			}}
		}
	}

	// “不喜欢”不记录为偏好，交给边界抽取处理。
	if strings.Contains(msg, "不喜欢") || strings.Contains(msg, "讨厌") {
		return nil
	}
	return nil
}

func inferPreferenceCategory(value string) string {
	v := strings.ToLower(value)
	switch {
	case strings.Contains(v, "歌") || strings.Contains(v, "音乐") || strings.Contains(v, "kpop") || strings.Contains(v, "jay"):
		return "music"
	case strings.Contains(v, "猫") || strings.Contains(v, "狗") || strings.Contains(v, "宠物"):
		return "pet"
	case strings.Contains(v, "吃") || strings.Contains(v, "寿司") || strings.Contains(v, "火锅") || strings.Contains(v, "咖啡"):
		return "food"
	case strings.Contains(v, "游戏") || strings.Contains(v, "dota") || strings.Contains(v, "lol"):
		return "game"
	default:
		return "general"
	}
}

func afterFirst(text, mark string) string {
	i := strings.Index(text, mark)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(text[i+len(mark):])
}

func trimMemoryValue(v string) string {
	stopMarks := []string{"。", "，", ",", "！", "!", "？", "?", "但是", "不过", "然后"}
	for _, mark := range stopMarks {
		if idx := strings.Index(v, mark); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
	}
	return strings.TrimSpace(v)
}

func dedupPreferences(items []PreferenceMemory) []PreferenceMemory {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]PreferenceMemory{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Category)) + "|" + strings.ToLower(strings.TrimSpace(item.Value))
		if old, ok := seen[key]; ok {
			if item.Confidence > old.Confidence {
				seen[key] = item
			}
			continue
		}
		seen[key] = item
	}
	out := make([]PreferenceMemory, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	return out
}

// extractPreferencesWithContext 处理“短回答”场景：结合 assistant 上一句问句推断偏好。
func extractPreferencesWithContext(msg, assistantMsg, conversationContext string) []PreferenceMemory {
	msg = strings.TrimSpace(msg)
	assistantMsg = strings.TrimSpace(assistantMsg)
	conversationContext = strings.TrimSpace(conversationContext)
	if msg == "" || assistantMsg == "" {
		return nil
	}
	// 已命中显式触发的情况由 extractPreferences 处理，这里只补短回答。
	if containsAny(msg, []string{"我喜欢", "我最喜欢", "我特别喜欢", "我爱", "我迷上了"}) {
		return nil
	}

	// 仅对较短回复启用推断，避免把长文本误判为偏好值。
	if runeLen(msg) > 20 {
		return nil
	}
	if !looksLikePreferenceQuestion(assistantMsg, conversationContext) {
		return nil
	}

	category := inferPreferenceCategoryWithContext(msg, assistantMsg+" "+conversationContext)
	value := trimMemoryValue(msg)
	if value == "" {
		return nil
	}
	return []PreferenceMemory{{
		Category:   category,
		Value:      value,
		Confidence: 0.74,
		Importance: 2,
	}}
}

func looksLikePreferenceQuestion(assistantMsg, conversationContext string) bool {
	text := strings.ToLower(strings.TrimSpace(assistantMsg + " " + conversationContext))
	if text == "" {
		return false
	}
	keywords := []string{
		"你喜欢", "最喜欢", "爱听", "想听", "听什么", "爱吃", "吃什么",
		"偏好", "平时玩什么", "喜欢玩", "喜欢哪", "更喜欢",
	}
	for _, kw := range keywords {
		if strings.Contains(text, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func inferPreferenceCategoryWithContext(value, context string) string {
	category := inferPreferenceCategory(value)
	if category != "general" {
		return category
	}
	ctx := strings.ToLower(strings.TrimSpace(context))
	switch {
	case strings.Contains(ctx, "音乐"), strings.Contains(ctx, "歌"), strings.Contains(ctx, "听"):
		return "music"
	case strings.Contains(ctx, "吃"), strings.Contains(ctx, "饭"), strings.Contains(ctx, "饮料"):
		return "food"
	case strings.Contains(ctx, "游戏"), strings.Contains(ctx, "玩"):
		return "game"
	case strings.Contains(ctx, "宠物"), strings.Contains(ctx, "猫"), strings.Contains(ctx, "狗"):
		return "pet"
	default:
		return "general"
	}
}

func runeLen(s string) int {
	return len([]rune(strings.TrimSpace(s)))
}

func containsAny(text string, keywords []string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, kw := range keywords {
		if strings.Contains(text, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
