package scoring

import "math"

// MemoryScore 计算通用记忆分值，供抽取阶段快速筛选使用。
func MemoryScore(importance int, confidence float64, recencyBoost float64) float64 {
	if importance < 1 {
		importance = 1
	}
	if importance > 5 {
		importance = 5
	}
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	if recencyBoost < 0 {
		recencyBoost = 0
	}
	if recencyBoost > 1 {
		recencyBoost = 1
	}
	return float64(importance)*10 + confidence*5 + recencyBoost
}

// DecayRecency 计算按天衰减的新鲜度分数。
func DecayRecency(ageDays float64, tau float64) float64 {
	if ageDays <= 0 {
		return 1
	}
	if tau <= 0 {
		tau = 30
	}
	return math.Exp(-ageDays / tau)
}
