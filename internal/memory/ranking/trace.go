package ranking

import "sort"

// traceBook 用于聚合同一候选在不同阶段的调试信息。
type traceBook map[string]*TraceItem

func newTraceBook() traceBook {
	return traceBook{}
}

func (tb traceBook) ensure(c Candidate) *TraceItem {
	if item, ok := tb[c.ID]; ok {
		return item
	}
	item := &TraceItem{
		CandidateID: c.ID,
		SourceID:    c.SourceID,
		Kind:        c.Kind,
		Topic:       c.Topic,
		Generated:   true,
		Notes:       []string{},
	}
	tb[c.ID] = item
	return item
}

func (tb traceBook) addNote(id string, note string) {
	if note == "" {
		return
	}
	item, ok := tb[id]
	if !ok {
		return
	}
	item.Notes = append(item.Notes, note)
}

func (tb traceBook) toSlice() []TraceItem {
	out := make([]TraceItem, 0, len(tb))
	for _, item := range tb {
		if len(item.Notes) == 0 {
			item.Notes = nil
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Selected != out[j].Selected {
			return out[i].Selected
		}
		if out[i].Rank != out[j].Rank {
			if out[i].Rank == 0 {
				return false
			}
			if out[j].Rank == 0 {
				return true
			}
			return out[i].Rank < out[j].Rank
		}
		return out[i].CandidateID < out[j].CandidateID
	})
	return out
}
