package extractor

import "strings"

// extractBoundaries 从用户消息识别边界/禁忌话题。
func extractBoundaries(msg string) []BoundaryMemory {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}

	triggers := []string{"别再提", "不要提", "别提", "不要聊", "我讨厌聊", "不要说"}
	for _, t := range triggers {
		if strings.Contains(msg, t) {
			topic := trimMemoryValue(afterFirst(msg, t))
			if topic == "" {
				topic = "sensitive_topic"
			}
			return []BoundaryMemory{{
				Topic:       normalizeBoundaryTopic(topic),
				Description: strings.TrimSpace(topic),
				Confidence:  0.9,
				Importance:  5,
			}}
		}
	}

	if strings.Contains(msg, "不喜欢") || strings.Contains(msg, "讨厌") {
		// 这类表达很多时候是偏好否定，也可作为软边界。
		topic := trimMemoryValue(afterFirst(msg, "不喜欢"))
		if topic == "" {
			topic = trimMemoryValue(afterFirst(msg, "讨厌"))
		}
		if topic != "" {
			return []BoundaryMemory{{
				Topic:       normalizeBoundaryTopic(topic),
				Description: "尽量避免：" + topic,
				Confidence:  0.72,
				Importance:  3,
			}}
		}
	}

	return nil
}

func normalizeBoundaryTopic(topic string) string {
	topic = strings.TrimSpace(strings.ToLower(topic))
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_", "，", "_", ",", "_")
	topic = replacer.Replace(topic)
	if topic == "" {
		return "sensitive_topic"
	}
	return topic
}

func dedupBoundaries(items []BoundaryMemory) []BoundaryMemory {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]BoundaryMemory{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Topic))
		if old, ok := seen[key]; ok {
			if item.Confidence > old.Confidence {
				seen[key] = item
			}
			continue
		}
		seen[key] = item
	}
	out := make([]BoundaryMemory, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	return out
}
