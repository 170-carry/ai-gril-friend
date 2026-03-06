package extractor

import (
	"regexp"
	"strings"
	"time"
)

// extractEvents 从用户消息识别未来事件。
func extractEvents(msg string, now time.Time) []EventMemory {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}

	if !looksLikeEvent(msg) {
		return nil
	}

	eventTime, ok := parseEventTime(msg, now)
	if !ok {
		return nil
	}
	title := inferEventTitle(msg)
	importance := inferEventImportance(msg)
	confidence := 0.78
	if strings.Contains(msg, "面试") || strings.Contains(msg, "考试") || strings.Contains(msg, "手术") {
		confidence = 0.88
	}

	return []EventMemory{{
		Title:      title,
		EventTime:  eventTime,
		Importance: importance,
		Confidence: confidence,
	}}
}

func looksLikeEvent(msg string) bool {
	keywords := []string{"明天", "后天", "周", "星期", "下周", "今天", "号", "面试", "考试", "开会", "复诊", "约会", "deadline", "ddl"}
	for _, kw := range keywords {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func parseEventTime(msg string, now time.Time) (time.Time, bool) {
	base := now
	hour, minute := parseClock(msg)

	if absolute, ok := parseAbsoluteDate(msg, now); ok {
		base = absolute
	} else {
		switch {
		case strings.Contains(msg, "明天"):
			base = truncateDay(now).AddDate(0, 0, 1)
		case strings.Contains(msg, "后天"):
			base = truncateDay(now).AddDate(0, 0, 2)
		case strings.Contains(msg, "今天"):
			base = truncateDay(now)
		default:
			if weekday, ok := parseWeekday(msg); ok {
				base = nextWeekday(truncateDay(now), weekday)
			} else {
				return time.Time{}, false
			}
		}
	}

	if hour < 0 {
		hour = 9
		minute = 0
	}
	return time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, base.Location()), true
}

func parseAbsoluteDate(msg string, now time.Time) (time.Time, bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return time.Time{}, false
	}
	loc := now.Location()

	// 1) yyyy-mm-dd / yyyy/mm/dd
	reYMD := regexp.MustCompile(`(\d{4})[/-](\d{1,2})[/-](\d{1,2})`)
	if groups := reYMD.FindStringSubmatch(msg); len(groups) == 4 {
		year := parseIntSafe(groups[1])
		month := parseIntSafe(groups[2])
		day := parseIntSafe(groups[3])
		if t, ok := buildValidDate(year, month, day, loc); ok {
			return t, true
		}
	}

	// 2) 3月10号 / 3月10日
	reCN := regexp.MustCompile(`(\d{1,2})月(\d{1,2})(?:日|号)?`)
	if groups := reCN.FindStringSubmatch(msg); len(groups) == 3 {
		month := parseIntSafe(groups[1])
		day := parseIntSafe(groups[2])
		year := now.Year()
		if t, ok := buildValidDate(year, month, day, loc); ok {
			if t.Before(truncateDay(now).AddDate(0, 0, -1)) {
				if next, ok := buildValidDate(year+1, month, day, loc); ok {
					return next, true
				}
			}
			return t, true
		}
	}

	// 3) 3/10（避免误匹配时间 xx:yy）
	reMD := regexp.MustCompile(`(^|[^\d])(\d{1,2})/(\d{1,2})([^\d]|$)`)
	if groups := reMD.FindStringSubmatch(msg); len(groups) == 5 {
		month := parseIntSafe(groups[2])
		day := parseIntSafe(groups[3])
		year := now.Year()
		if t, ok := buildValidDate(year, month, day, loc); ok {
			if t.Before(truncateDay(now).AddDate(0, 0, -1)) {
				if next, ok := buildValidDate(year+1, month, day, loc); ok {
					return next, true
				}
			}
			return t, true
		}
	}

	return time.Time{}, false
}

func buildValidDate(year, month, day int, loc *time.Location) (time.Time, bool) {
	if year < 1970 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

// extractEventsWithContext 处理 assistant 追问后的“时间短回复”场景。
func extractEventsWithContext(msg, assistantMsg string, now time.Time) []EventMemory {
	msg = strings.TrimSpace(msg)
	assistantMsg = strings.TrimSpace(assistantMsg)
	if msg == "" || assistantMsg == "" {
		return nil
	}
	if looksLikeEvent(msg) {
		return nil
	}
	text := strings.ToLower(assistantMsg)
	if !strings.Contains(text, "什么时候") && !strings.Contains(text, "哪天") && !strings.Contains(text, "几点") {
		return nil
	}
	when, ok := parseEventTime(msg, now)
	if !ok {
		return nil
	}
	return []EventMemory{{
		Title:      "待确认事件",
		EventTime:  when,
		Importance: 3,
		Confidence: 0.7,
	}}
}

func parseClock(msg string) (int, int) {
	msg = strings.TrimSpace(msg)
	for i := 0; i < len(msg); i++ {
		if msg[i] < '0' || msg[i] > '9' {
			continue
		}
		j := i
		for j < len(msg) && msg[j] >= '0' && msg[j] <= '9' {
			j++
		}
		num := parseIntSafe(msg[i:j])
		if num < 0 || num > 23 {
			continue
		}
		if j < len(msg) && msg[j] == ':' {
			k := j + 1
			for k < len(msg) && msg[k] >= '0' && msg[k] <= '9' {
				k++
			}
			minute := parseIntSafe(msg[j+1 : k])
			if minute >= 0 && minute < 60 {
				return num, minute
			}
		}
		// 这里不能用 byte 与中文 rune 直接比较，否则会触发编译溢出。
		if j < len(msg) && strings.HasPrefix(msg[j:], "点") {
			return num, 0
		}
	}
	return -1, -1
}

func parseWeekday(msg string) (time.Weekday, bool) {
	pairs := map[string]time.Weekday{
		"周日":  time.Sunday,
		"周天":  time.Sunday,
		"周一":  time.Monday,
		"周二":  time.Tuesday,
		"周三":  time.Wednesday,
		"周四":  time.Thursday,
		"周五":  time.Friday,
		"周六":  time.Saturday,
		"星期日": time.Sunday,
		"星期天": time.Sunday,
		"星期一": time.Monday,
		"星期二": time.Tuesday,
		"星期三": time.Wednesday,
		"星期四": time.Thursday,
		"星期五": time.Friday,
		"星期六": time.Saturday,
	}
	for k, v := range pairs {
		if strings.Contains(msg, k) {
			return v, true
		}
	}
	return 0, false
}

func nextWeekday(base time.Time, wd time.Weekday) time.Time {
	delta := int(wd - base.Weekday())
	if delta <= 0 {
		delta += 7
	}
	return base.AddDate(0, 0, delta)
}

func inferEventTitle(msg string) string {
	switch {
	case strings.Contains(msg, "面试"):
		return "面试"
	case strings.Contains(msg, "考试"):
		return "考试"
	case strings.Contains(msg, "复诊") || strings.Contains(msg, "看医生"):
		return "就医"
	case strings.Contains(msg, "开会"):
		return "会议"
	default:
		short := trimMemoryValue(msg)
		if short == "" {
			return "待办事件"
		}
		return short
	}
}

func inferEventImportance(msg string) int {
	switch {
	case strings.Contains(msg, "面试"), strings.Contains(msg, "手术"), strings.Contains(msg, "高考"), strings.Contains(msg, "deadline"):
		return 4
	case strings.Contains(msg, "考试"), strings.Contains(msg, "复诊"), strings.Contains(msg, "答辩"):
		return 4
	default:
		return 3
	}
}

func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func parseIntSafe(s string) int {
	if strings.TrimSpace(s) == "" {
		return -1
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func dedupEvents(items []EventMemory) []EventMemory {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]EventMemory{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Title)) + "|" + item.EventTime.Format("2006-01-02 15:04")
		if old, ok := seen[key]; ok {
			if item.Confidence > old.Confidence {
				seen[key] = item
			}
			continue
		}
		seen[key] = item
	}
	out := make([]EventMemory, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	return out
}
