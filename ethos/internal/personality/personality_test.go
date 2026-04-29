package personality

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGreeting_AllLevels(t *testing.T) {
	tests := []struct {
		name        string
		level       Level
		contains    string
		notContains string
	}{
		{"off returns Ready", LevelOff, "Ready.", ""},
		{"subtle contains Hey", LevelSubtle, "Hey", ""},
		{"witty contains grind", LevelWitty, "grind", ""},
		{"full contains agent name", LevelFull, "Bot", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{Level: tt.level, AgentName: "Bot", UserName: "Alice"})
			got := p.Greeting("morning")
			assert.Contains(t, got, tt.contains)
			if tt.notContains != "" {
				assert.NotContains(t, got, tt.notContains)
			}
		})
	}
}

func TestErrorResponse_AllLevels(t *testing.T) {
	err := errors.New("connection refused")

	tests := []struct {
		name     string
		level    Level
		contains string
	}{
		{"off prefixes Error", LevelOff, "Error: connection refused"},
		{"subtle says Welp", LevelSubtle, "Welp"},
		{"witty says broken", LevelWitty, "broken"},
		{"full says failed you", LevelFull, "failed you"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{Level: tt.level, AgentName: "Bot"})
			got := p.ErrorResponse(err)
			assert.Contains(t, got, tt.contains)
		})
	}
}

func TestErrorResponse_NilError(t *testing.T) {
	p := New(Config{Level: LevelSubtle})
	assert.Empty(t, p.ErrorResponse(nil))
}

func TestSuccessResponse_AllLevels(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		contains string
	}{
		{"off says Done", LevelOff, "Done:"},
		{"subtle says Got it", LevelSubtle, "Got it"},
		{"witty says Nailed it", LevelWitty, "Nailed it"},
		{"full says delivers", LevelFull, "delivers"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{Level: tt.level, AgentName: "Bot"})
			got := p.SuccessResponse("deploy")
			assert.Contains(t, got, tt.contains)
		})
	}
}

func TestBootMessage(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		contains string
	}{
		{"off", LevelOff, "System initialized"},
		{"subtle", LevelSubtle, "online"},
		{"witty", LevelWitty, "booting up"},
		{"full", LevelFull, "ALIVE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{Level: tt.level, AgentName: "Bot", UserName: "Alice"})
			got := p.BootMessage()
			assert.Contains(t, got, tt.contains)
		})
	}
}

func TestInjectPersonality_AllLevels(t *testing.T) {
	basePrompt := "You are a helpful assistant."

	tests := []struct {
		name        string
		level       Level
		shouldAdd   bool
		contains    string
		notContains string
	}{
		{"off adds nothing", LevelOff, false, "", "voice"},
		{"subtle adds voice instruction", LevelSubtle, true, "voice", ""},
		{"witty adds humor instruction", LevelWitty, true, "witty", ""},
		{"full adds full personality", LevelFull, true, "Full personality", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{Level: tt.level})
			got := p.InjectPersonality(basePrompt)
			assert.Contains(t, got, basePrompt)
			if tt.shouldAdd {
				assert.NotEqual(t, basePrompt, got)
			} else {
				assert.Equal(t, basePrompt, got)
			}
			if tt.contains != "" {
				assert.Contains(t, got, tt.contains)
			}
			if tt.notContains != "" {
				assert.NotContains(t, got, tt.notContains)
			}
		})
	}
}

func TestRelationshipTracker_RecordBeat(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordBeat(BeatFirstFailure, "test context", "session-1")

	state := rt.State()
	require.Len(t, state.Beats, 1)
	assert.Equal(t, BeatFirstFailure, state.Beats[0].Type)
	assert.Equal(t, "test context", state.Beats[0].Context)
	assert.Equal(t, "session-1", state.Beats[0].SessionID)
	assert.False(t, state.FirstSeen.IsZero())
	assert.False(t, state.LastSeen.IsZero())
}

func TestRelationshipTracker_RecordBeat_SetsMilestone(t *testing.T) {
	rt := NewRelationshipTracker()
	assert.False(t, rt.HasMilestone(BeatFirstSuccess))

	rt.RecordBeat(BeatFirstSuccess, "did it", "s1")
	assert.True(t, rt.HasMilestone(BeatFirstSuccess))
}

func TestRelationshipTracker_Milestones(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordBeat(BeatFirstPR, "merged PR #1", "s1")

	assert.True(t, rt.HasMilestone(BeatFirstPR))
	assert.False(t, rt.HasMilestone(BeatFirstFailure))

	state := rt.State()
	assert.True(t, state.Milestones[BeatFirstPR])
	assert.False(t, state.Milestones[BeatFirstFailure])
}

func TestRelationshipTracker_Opener_FirstSession(t *testing.T) {
	rt := NewRelationshipTracker()
	got := rt.Opener("Butter", "Alice", "")
	assert.Contains(t, got, "Butter")
	assert.Contains(t, got, "Alice")
	assert.Contains(t, got, "build something")
}

func TestRelationshipTracker_Opener_Returning(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.IncrementSession()
	rt.RecordBeat(BeatFirstSuccess, "deployed", "s1")

	got := rt.Opener("Butter", "Alice", "auth module")
	assert.Contains(t, got, "auth module")
}

func TestRelationshipTracker_Opener_ReturningAfterFrustration(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.IncrementSession()
	rt.RecordBeat(BeatFrustration, "user was angry", "s1")

	got := rt.Opener("Butter", "Alice", "")
	assert.Contains(t, got, "take it easy")
}

func TestRelationshipTracker_Opener_LateNight(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.IncrementSession()

	got := rt.Opener("Butter", "Alice", "")
	assert.NotEmpty(t, got)
}

func TestRelationshipTracker_Opener_AfterMilestone(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.IncrementSession()
	rt.RecordBeat(BeatFirstPR, "merged PR #1", "s1")

	got := rt.Opener("Butter", "Alice", "")
	assert.Contains(t, got, "shipped a PR")
}

func TestRelationshipTracker_ConcurrentAccess(t *testing.T) {
	rt := NewRelationshipTracker()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			rt.RecordBeat(BeatFirstFailure, "concurrent beat", "session-concurrent")
			rt.RecordInteraction()
			_ = rt.State()
			_ = rt.HasMilestone(BeatFirstFailure)
			_ = rt.SessionCount()
		}(i)
	}

	wg.Wait()

	state := rt.State()
	assert.Len(t, state.Beats, 100)
	assert.Equal(t, 100, state.TotalInteractions)
}

func TestRelationshipTracker_RecordInteraction(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordInteraction()
	rt.RecordInteraction()
	rt.RecordInteraction()

	state := rt.State()
	assert.Equal(t, 3, state.TotalInteractions)
}

func TestRelationshipTracker_IncrementSession(t *testing.T) {
	rt := NewRelationshipTracker()
	assert.Equal(t, 0, rt.SessionCount())

	rt.IncrementSession()
	assert.Equal(t, 1, rt.SessionCount())

	rt.IncrementSession()
	assert.Equal(t, 2, rt.SessionCount())
}

func TestSoulFile_LoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nonexistent", "soul.md")

	soul, err := LoadSoul(path)
	require.NoError(t, err)
	assert.NotNil(t, soul)
	assert.False(t, soul.Exists)
	assert.Empty(t, soul.GetContent())
}

func TestSoulFile_CreateAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "soul.md")

	err := CreateDefaultSoul(path, "Butter")
	require.NoError(t, err)

	soul, err := LoadSoul(path)
	require.NoError(t, err)
	assert.True(t, soul.Exists)
	assert.Contains(t, soul.GetContent(), "Butter")
	assert.Contains(t, soul.GetContent(), "Core Traits")
}

func TestSoulFile_CreateDefaultSoul_CreatesDirs(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nested", "deep", "soul.md")

	err := CreateDefaultSoul(path, "Bot")
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.NoError(t, statErr)
}

func TestSoulFile_Update(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "soul.md")

	soul, err := LoadSoul(path)
	require.NoError(t, err)
	soul.Path = path

	newContent := "# Updated Soul\n\nNew content here."
	err = soul.Update(newContent)
	require.NoError(t, err)
	assert.Equal(t, newContent, soul.GetContent())
	assert.True(t, soul.Exists)

	loaded, err := LoadSoul(path)
	require.NoError(t, err)
	assert.Equal(t, newContent, loaded.GetContent())
}

func TestSoulFile_Update_EmptyPath(t *testing.T) {
	soul := &SoulFile{Path: "", Content: "", Exists: false}
	err := soul.Update("content")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path is empty")
}

func TestFunFactDB_Random(t *testing.T) {
	db := NewFunFactDB()
	got := db.Random()
	assert.NotEmpty(t, got)
}

func TestFunFactDB_Random_MultipleCallsVary(t *testing.T) {
	db := NewFunFactDB()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		seen[db.Random()] = true
	}
	assert.Greater(t, len(seen), 1, "should get varied facts over 50 calls")
}

func TestFunFactDB_ForTime(t *testing.T) {
	db := NewFunFactDB()

	got := db.ForTime(2)
	assert.NotEmpty(t, got)

	got = db.ForTime(14)
	assert.NotEmpty(t, got)

	got = db.ForTime(8)
	assert.NotEmpty(t, got)
}

func TestFunFactDB_ForTime_MatchesSleepCategory(t *testing.T) {
	db := NewFunFactDB()

	fact := db.ForTime(3)
	assert.NotEmpty(t, fact)

	allFacts := db.facts
	found := false
	for _, f := range allFacts {
		if f.Fact == fact {
			found = true
			break
		}
	}
	assert.True(t, found, "returned fact should be from the database")
}

func TestFunFactDB_ForContext(t *testing.T) {
	db := NewFunFactDB()

	got := db.ForContext("debug")
	assert.NotEmpty(t, got)

	got = db.ForContext("coding")
	assert.NotEmpty(t, got)

	got = db.ForContext("sleep")
	assert.NotEmpty(t, got)

	got = db.ForContext("nonexistent_category")
	assert.NotEmpty(t, got, "should fallback to random for unknown context")
}

func TestFunFactDB_ForContext_DebugReturnsDebugFact(t *testing.T) {
	db := NewFunFactDB()

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		fact := db.ForContext("debug")
		seen[fact] = true
	}

	for fact := range seen {
		isDebug := false
		for _, f := range db.facts {
			if f.Fact == fact && f.Category == "debug" {
				isDebug = true
				break
			}
		}
		assert.True(t, isDebug, "ForContext(debug) should return debug facts, got: %s", fact)
	}
}

func TestFunFactDB_Count(t *testing.T) {
	db := NewFunFactDB()
	assert.GreaterOrEqual(t, db.Count(), 20)
}

func TestConfig_AgentName(t *testing.T) {
	p := New(Config{AgentName: "Butter", UserName: "Alice"})
	assert.Equal(t, "Butter", p.AgentName())
	assert.Equal(t, "Alice", p.UserName())
}

func TestConfig_DefaultLanguage(t *testing.T) {
	p := New(Config{})
	assert.Equal(t, "en", p.config.Language)
}

func TestPersonality_Defaults(t *testing.T) {
	p := New(Config{})
	assert.Equal(t, LevelOff, p.GetLevel())
	assert.NotNil(t, p.Relationship())
	assert.NotNil(t, p.FunFacts())
	assert.NotNil(t, p.Soul())
}

func TestPersonality_DefaultLevelIsSubtle_WhenConfigured(t *testing.T) {
	p := New(Config{Level: LevelSubtle})
	assert.Equal(t, LevelSubtle, p.GetLevel())
}

func TestPersonality_LoadSoulFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "soul.md")

	err := CreateDefaultSoul(path, "Butter")
	require.NoError(t, err)

	p := New(Config{AgentName: "Butter"})
	err = p.LoadSoulFile(path)
	require.NoError(t, err)
	assert.True(t, p.Soul().Exists)
	assert.Contains(t, p.Soul().GetContent(), "Butter")
}

func TestPersonality_LoadSoulFile_Missing(t *testing.T) {
	p := New(Config{})
	err := p.LoadSoulFile("/nonexistent/path/soul.md")
	require.NoError(t, err)
	assert.False(t, p.Soul().Exists)
}

func TestGreeting_WithUserName(t *testing.T) {
	p := New(Config{Level: LevelSubtle, UserName: "Bob"})
	got := p.Greeting("afternoon")
	assert.Contains(t, got, "Bob")
}

func TestGreeting_WithoutUserName(t *testing.T) {
	p := New(Config{Level: LevelSubtle, UserName: ""})
	got := p.Greeting("afternoon")
	assert.Contains(t, got, "Hey")
	assert.NotContains(t, got, "  ")
}

func TestSuccessResponse_ContainsTask(t *testing.T) {
	p := New(Config{Level: LevelSubtle})
	got := p.SuccessResponse("deployed to production")
	assert.Contains(t, got, "deployed to production")
}

func TestBootMessage_ContainsAgentName(t *testing.T) {
	p := New(Config{Level: LevelSubtle, AgentName: "Maverick"})
	got := p.BootMessage()
	assert.Contains(t, got, "Maverick")
}

func TestInjectPersonality_OffReturnsSamePrompt(t *testing.T) {
	p := New(Config{Level: LevelOff})
	prompt := "You are helpful."
	assert.Equal(t, prompt, p.InjectPersonality(prompt))
}

func TestRelationshipTracker_MultipleBeats(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordBeat(BeatFirstFailure, "fail", "s1")
	rt.RecordBeat(BeatFirstSuccess, "success", "s1")
	rt.RecordBeat(BeatFirstPR, "merged", "s2")

	state := rt.State()
	assert.Len(t, state.Beats, 3)
	assert.True(t, state.Milestones[BeatFirstFailure])
	assert.True(t, state.Milestones[BeatFirstSuccess])
	assert.True(t, state.Milestones[BeatFirstPR])
}

func TestRelationshipTracker_FirstSeenSetOnce(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordBeat(BeatFirstFailure, "ctx", "s1")
	firstSeen := rt.State().FirstSeen

	time.Sleep(10 * time.Millisecond)
	rt.RecordBeat(BeatFirstSuccess, "ctx", "s2")

	state := rt.State()
	assert.Equal(t, firstSeen, state.FirstSeen, "FirstSeen should not change")
	assert.True(t, state.LastSeen.After(firstSeen))
}

func TestSoulFile_DefaultTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "soul.md")

	err := CreateDefaultSoul(path, "Butter")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Butter's Soul")
	assert.Contains(t, content, "Core Traits")
	assert.Contains(t, content, "Honest about limitations")
	assert.Contains(t, content, "Direct, not sycophantic")
	assert.Contains(t, content, "Colleague, not servant")
	assert.Contains(t, content, "What I Know")
	assert.Contains(t, content, "What I Can't Do")
}

func TestLevel_Constants(t *testing.T) {
	assert.Equal(t, Level(0), LevelOff)
	assert.Equal(t, Level(1), LevelSubtle)
	assert.Equal(t, Level(2), LevelWitty)
	assert.Equal(t, Level(3), LevelFull)
}

func TestFunFactDB_Categories(t *testing.T) {
	db := NewFunFactDB()
	categories := map[string]bool{}
	for _, f := range db.facts {
		categories[f.Category] = true
	}
	assert.True(t, categories["general"])
	assert.True(t, categories["coding"])
	assert.True(t, categories["sleep"])
	assert.True(t, categories["monday"])
	assert.True(t, categories["debug"])
	assert.True(t, categories["late"])
}

func TestRelationshipTracker_StateReturnsCopy(t *testing.T) {
	rt := NewRelationshipTracker()
	rt.RecordBeat(BeatFirstFailure, "ctx", "s1")

	state := rt.State()
	state.Beats[0].Context = "modified"

	original := rt.State()
	assert.Equal(t, "ctx", original.Beats[0].Context, "State() should return a copy")
}

func TestErrorResponse_ContainsOriginalError(t *testing.T) {
	err := errors.New("specific error XYZ123")
	p := New(Config{Level: LevelWitty})
	got := p.ErrorResponse(err)
	assert.Contains(t, got, "specific error XYZ123")
}

func TestGreeting_FullLevel_ContainsFunFactOrAgentName(t *testing.T) {
	p := New(Config{Level: LevelFull, AgentName: "Bot", UserName: "Alice"})
	got := p.Greeting("morning")
	assert.True(t, strings.Contains(got, "Bot") || len(got) > 10)
}
