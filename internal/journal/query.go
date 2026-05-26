package journal

import "strings"

type JournalQuery struct {
	store *ObservationStore
}

func NewJournalQuery(store *ObservationStore) *JournalQuery {
	return &JournalQuery{store: store}
}

// maxSearchScan caps how many observation indices Search inspects per
// query. Without a ceiling, every search loaded the entire observation
// table into memory — fine at hundreds, painful at the hundreds of
// thousands a long-running agent accumulates. We pre-filter by obsType
// in the List call when possible (the store can index that cheaply)
// and only scan up to maxSearchScan candidates for the title-substring
// match. Old observations are LRU-evicted from the search by virtue
// of List returning most-recent-first.
const maxSearchScan = 5000

func (jq *JournalQuery) Search(query string, obsType ObservationType, limit int) []ObservationIndex {
	if query == "" {
		return jq.store.List(obsType, limit)
	}

	// Push the type filter into the store layer (cheap) and bound the
	// candidate set so we don't materialise an unbounded slice in RAM.
	indices := jq.store.List(obsType, maxSearchScan)

	var result []ObservationIndex
	lower := strings.ToLower(query)
	for _, idx := range indices {
		if !strings.Contains(strings.ToLower(idx.Title), lower) {
			continue
		}
		result = append(result, idx)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result
}

func (jq *JournalQuery) Timeline(anchorID string, depth int) []Observation {
	return jq.store.Timeline(anchorID, depth)
}

func (jq *JournalQuery) Get(id string) (*Observation, error) {
	return jq.store.Get(id)
}
