package ranking

import (
	"sort"
	"strings"
)

// selectWithMMR 使用 MMR + topic 限流做多样性选择。
func (r *Ranker) selectWithMMR(req RankRequest, scored []scoredCandidate, traces traceBook) []scoredCandidate {
	if len(scored) == 0 {
		return nil
	}
	k := req.K
	if k <= 0 {
		k = r.cfg.OutputK
	}
	if k <= 0 {
		k = 5
	}

	topicCount := map[string]int{}
	selected := make([]scoredCandidate, 0, k)
	remaining := make([]scoredCandidate, 0, len(scored))
	remaining = append(remaining, scored...)

	for len(remaining) > 0 && len(selected) < k {
		bestIdx := -1
		bestMMR := -1e9
		for i := range remaining {
			cand := remaining[i]
			topic := normalizeTopic(cand.Topic)
			if topicCount[topic] >= r.cfg.TopicMaxPer {
				continue
			}

			redundancy := maxSimilarityToSelected(cand, selected)
			mmr := r.cfg.MMRLambda*cand.Score - (1-r.cfg.MMRLambda)*redundancy
			if mmr > bestMMR {
				bestMMR = mmr
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			break
		}

		pick := remaining[bestIdx]
		selected = append(selected, pick)
		topicCount[normalizeTopic(pick.Topic)]++
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].Score > selected[j].Score
	})
	for i, item := range selected {
		t := traces.ensure(item.Candidate)
		t.Selected = true
		t.Rank = i + 1
	}
	return selected
}

func maxSimilarityToSelected(c scoredCandidate, selected []scoredCandidate) float64 {
	if len(selected) == 0 {
		return 0
	}
	maxSim := 0.0
	for _, s := range selected {
		sim := lexicalSimilarity(c.ContentShort, s.ContentShort)
		if sim > maxSim {
			maxSim = sim
		}
	}
	return maxSim
}

func normalizeTopic(topic string) string {
	topic = strings.TrimSpace(strings.ToLower(topic))
	if topic == "" {
		return "general"
	}
	return topic
}
