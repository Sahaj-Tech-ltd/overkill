package memory

import (
	"strings"
	"time"
)

func tokenize(s string) []string {
	s = strings.ToLower(s)
	return strings.Fields(s)
}

func scoreContent(query, content string) float64 {
	if query == "" {
		return 0
	}

	queryTerms := tokenize(query)
	contentLower := strings.ToLower(content)

	hits := 0
	for _, term := range queryTerms {
		if strings.Contains(contentLower, term) {
			hits++
		}
	}

	if len(queryTerms) == 0 {
		return 0
	}

	return float64(hits) / float64(len(queryTerms))
}

func containsType(types []MemoryType, t MemoryType) bool {
	for _, mt := range types {
		if mt == t {
			return true
		}
	}
	return false
}

var timeNow = func() time.Time {
	return time.Now().UTC()
}

func containsAnyTag(queryTags, memoryTags []string) bool {
	tagSet := make(map[string]struct{}, len(memoryTags))
	for _, t := range memoryTags {
		tagSet[t] = struct{}{}
	}
	for _, t := range queryTags {
		if _, ok := tagSet[t]; ok {
			return true
		}
	}
	return false
}
