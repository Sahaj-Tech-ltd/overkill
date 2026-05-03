package journal

import "strings"

type JournalQuery struct {
	store *ObservationStore
}

func NewJournalQuery(store *ObservationStore) *JournalQuery {
	return &JournalQuery{store: store}
}

func (jq *JournalQuery) Search(query string, obsType ObservationType, limit int) []ObservationIndex {
	if query == "" {
		return jq.store.List(obsType, limit)
	}

	indices := jq.store.List("", 0)

	var result []ObservationIndex
	lower := strings.ToLower(query)
	for _, idx := range indices {
		if obsType != "" && idx.Type != obsType {
			continue
		}
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
