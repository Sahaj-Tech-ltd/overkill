package plan

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLearningsStore_AppendThenAll(t *testing.T) {
	s := NewLearningsStore(filepath.Join(t.TempDir(), "l"))
	if err := s.Append(Learning{Topic: "auth", Lesson: "csrf needs scope check"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Learning{Topic: "cache", Lesson: "redis miss rate too high under cold start"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 records, got %d", len(all))
	}
	for _, l := range all {
		if l.ID == "" {
			t.Errorf("ID should be auto-assigned: %+v", l)
		}
		if l.Timestamp.IsZero() {
			t.Errorf("timestamp should be auto-assigned: %+v", l)
		}
	}
}

func TestLearningsStore_SearchMatchesTopicLessonTags(t *testing.T) {
	s := NewLearningsStore(filepath.Join(t.TempDir(), "l"))
	_ = s.Append(Learning{Topic: "auth", Lesson: "lesson A", Tags: []string{"security"}})
	_ = s.Append(Learning{Topic: "cache", Lesson: "csrf scoping", Tags: []string{"perf"}})
	_ = s.Append(Learning{Topic: "ui", Lesson: "lesson B", Tags: []string{"a11y"}})

	if hits, _ := s.Search("auth"); len(hits) != 1 {
		t.Errorf("'auth' topic match: %+v", hits)
	}
	if hits, _ := s.Search("csrf"); len(hits) != 1 {
		t.Errorf("'csrf' lesson match: %+v", hits)
	}
	if hits, _ := s.Search("a11y"); len(hits) != 1 {
		t.Errorf("'a11y' tag match: %+v", hits)
	}
	if hits, _ := s.Search(""); len(hits) != 0 {
		t.Errorf("empty query should return no hits, got %+v", hits)
	}
}

func TestLearningsStore_SearchForModelFiltersAndIncludesUnversioned(t *testing.T) {
	s := NewLearningsStore(filepath.Join(t.TempDir(), "l"))
	now := time.Now().UTC()
	_ = s.Append(Learning{Topic: "x", Lesson: "claude lesson", ModelID: "claude-opus-4-7", Timestamp: now})
	_ = s.Append(Learning{Topic: "y", Lesson: "gpt lesson", ModelID: "gpt-5.4", Timestamp: now})
	_ = s.Append(Learning{Topic: "z", Lesson: "unversioned lesson", Timestamp: now})

	hits, err := s.SearchForModel("lesson", "claude-opus-4-7")
	if err != nil {
		t.Fatal(err)
	}
	topics := map[string]bool{}
	for _, l := range hits {
		topics[l.Topic] = true
	}
	if !topics["x"] || !topics["z"] {
		t.Errorf("expected claude and unversioned, got %+v", topics)
	}
	if topics["y"] {
		t.Errorf("gpt record should be filtered out: %+v", topics)
	}
}

func TestLearningsStore_AllOnMissingDirIsNil(t *testing.T) {
	s := NewLearningsStore(filepath.Join(t.TempDir(), "nope"))
	got, err := s.All()
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if got != nil {
		t.Errorf("missing dir should return nil, got %+v", got)
	}
}
