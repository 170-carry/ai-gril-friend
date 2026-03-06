package dedup

import "strings"

// NormalizeText 将文本标准化，用于稳定去重键。
func NormalizeText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		"，", ",",
		"。", ".",
		"；", ";",
	)
	s = replacer.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// PreferenceKey 生成偏好条目的去重键。
func PreferenceKey(category, value string) string {
	return NormalizeText(category) + "|" + NormalizeText(value)
}

// BoundaryKey 生成边界条目的去重键。
func BoundaryKey(topic string) string {
	return NormalizeText(topic)
}

// SemanticKey 生成语义记忆条目的去重键。
func SemanticKey(content string) string {
	return NormalizeText(content)
}
