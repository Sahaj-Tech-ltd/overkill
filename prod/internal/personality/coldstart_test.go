package personality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsColdStart_ReturnsTrueForEmptyPath(t *testing.T) {
	csp := NewColdStartProtocol()
	assert.True(t, csp.IsColdStart(""))
}

func TestIsColdStart_ReturnsTrueForNonexistentFile(t *testing.T) {
	csp := NewColdStartProtocol()
	assert.True(t, csp.IsColdStart("/nonexistent/path/relationship.json"))
}

func TestIsColdStart_ReturnsFalseForExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "relationship.json")
	err := os.WriteFile(path, []byte(`{"total_sessions": 5}`), 0600)
	require.NoError(t, err)

	csp := NewColdStartProtocol()
	assert.False(t, csp.IsColdStart(path))
}

func TestIsColdStart_ReturnsTrueForEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "relationship.json")
	err := os.WriteFile(path, []byte{}, 0600)
	require.NoError(t, err)

	csp := NewColdStartProtocol()
	assert.True(t, csp.IsColdStart(path))
}

func TestOpeningQuestion_ReturnsNonEmpty(t *testing.T) {
	csp := NewColdStartProtocol()
	q := csp.OpeningQuestion()
	assert.NotEmpty(t, q)
	assert.Contains(t, q, "I don't know you yet")
}

func TestProcessResponse_InfersTerseFromShort(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Fixing a bug")
	assert.Equal(t, "terse", profile.VerbosityPreference)
	assert.Equal(t, "direct", profile.CommunicationStyle)
}

func TestProcessResponse_InfersModerateFromMedium(t *testing.T) {
	csp := NewColdStartProtocol()
	response := "I am working on a web application that needs some refactoring. The auth module has a few issues that need to be addressed before the next release cycle."
	profile := csp.ProcessResponse(response)
	assert.Equal(t, "moderate", profile.VerbosityPreference)
	assert.Equal(t, "contextual", profile.CommunicationStyle)
}

func TestProcessResponse_InfersVerboseFromLong(t *testing.T) {
	csp := NewColdStartProtocol()
	words := make([]string, 120)
	for i := range words {
		words[i] = "word"
	}
	response := strings.Join(words, " ")
	profile := csp.ProcessResponse(response)
	assert.Equal(t, "verbose", profile.VerbosityPreference)
	assert.Equal(t, "verbose", profile.CommunicationStyle)
}

func TestProcessResponse_InfersTechnicalDepth(t *testing.T) {
	csp := NewColdStartProtocol()
	response := "Working on `main.go` with utils.parse_args and config_loader in /cmd/overkill/main.go using pkg/api/handler.go and internal/tools/shell.go plus middleware.go"
	profile := csp.ProcessResponse(response)
	assert.Equal(t, "high", profile.TechnicalDepth)
}

func TestProcessResponse_InfersLowTechnicalDepth(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Just getting started with my project")
	assert.Equal(t, "low", profile.TechnicalDepth)
}

func TestProcessResponse_InfersCasualTone(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Hey yeah I'm building something cool, it's a fun project with lots of moving parts")
	assert.Equal(t, "casual", profile.ToneTolerance)
}

func TestProcessResponse_InfersFormalTone(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("I would like to build a service. Could you please help me with the architecture design?")
	assert.Equal(t, "formal", profile.ToneTolerance)
}

func TestProcessResponse_InfersModerateToneDefault(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Building a new API endpoint for user authentication")
	assert.Equal(t, "moderate", profile.ToneTolerance)
}

func TestProcessResponse_InfersUrgency(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("I need this done ASAP, urgent deadline today, need to ship quickly before the sprint ends")
	assert.Equal(t, "high", profile.UrgencyBaseline)
}

func TestProcessResponse_InfersLowUrgency(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Whenever you have time, no rush. Just some cleanup eventually when you can")
	assert.Equal(t, "low", profile.UrgencyBaseline)
}

func TestProcessResponse_ExtractsUserName(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{"I'm pattern", "Hey, I'm John and I'm working on a Go project", "John"},
		{"my name is pattern", "Hello, my name is Alice and I need help", "Alice"},
		{"call me pattern", "You can just call me Bob, working on a project", "Bob"},
		{"no name", "Working on a project with no personal info", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csp := NewColdStartProtocol()
			profile := csp.ProcessResponse(tt.response)
			assert.Equal(t, tt.expected, profile.UserName)
		})
	}
}

func TestProcessResponse_ExtractsTimezone(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{"EST mention", "I'm in EST and working on a project", "EST"},
		{"PST here", "PST here, building something", "PST"},
		{"UTC offset", "I'm at UTC+5, working late", "UTC+5"},
		{"no timezone", "Working on a project without location info", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csp := NewColdStartProtocol()
			profile := csp.ProcessResponse(tt.response)
			assert.Equal(t, tt.expected, profile.Timezone)
		})
	}
}

func TestProcessResponse_SetsStateComplete(t *testing.T) {
	csp := NewColdStartProtocol()
	assert.Equal(t, ColdStartUnknown, csp.State())
	csp.ProcessResponse("Just building a project")
	assert.Equal(t, ColdStartComplete, csp.State())
}

func TestToRelationshipArc_ProducesValidMap(t *testing.T) {
	csp := NewColdStartProtocol()
	profile := csp.ProcessResponse("Hey, I'm Dave. Building a Go API with `main.go` and need it ASAP. I'm in PST.")
	arc := profile.ToRelationshipArc()

	expectedKeys := []string{"communication", "verbosity", "technical_depth", "tone", "urgency", "user_name", "timezone"}
	for _, key := range expectedKeys {
		_, exists := arc[key]
		assert.True(t, exists, "ToRelationshipArc should contain key: %s", key)
	}

	assert.Equal(t, "Dave", arc["user_name"])
	assert.Equal(t, "PST", arc["timezone"])
	assert.Equal(t, "casual", arc["tone"])
	assert.Equal(t, "high", arc["urgency"])
}

func TestNewColdStartProtocol_InitialState(t *testing.T) {
	csp := NewColdStartProtocol()
	assert.Equal(t, ColdStartUnknown, csp.State())
	assert.NotNil(t, csp.profile)
}

func TestColdStartProtocol_SetState(t *testing.T) {
	csp := NewColdStartProtocol()
	assert.Equal(t, ColdStartUnknown, csp.State())

	csp.SetState(ColdStartPending)
	assert.Equal(t, ColdStartPending, csp.State())

	csp.SetState(ColdStartComplete)
	assert.Equal(t, ColdStartComplete, csp.State())
}

func TestProcessResponse_Comprehensive(t *testing.T) {
	csp := NewColdStartProtocol()
	response := "Hey yeah, I'm Marcus. I'm working on a Go microservice with `cmd/server/main.go` and internal/handlers/api.go plus pkg/middleware/auth.go, also using tools/generate.go, config/loader.go, internal/db/queries.sql, and models/user.go. Need this shipped ASAP for a deadline today. I'm in EST. This is a big project with lots of moving parts and we need to get it done quickly with high quality and good test coverage across all the packages and modules and services. The team is counting on me to deliver this feature before the end of the sprint so we really need to move fast and make sure everything is solid and well tested and ready for production deployment."
	profile := csp.ProcessResponse(response)

	assert.Equal(t, "verbose", profile.VerbosityPreference)
	assert.Equal(t, "verbose", profile.CommunicationStyle)
	assert.Equal(t, "high", profile.TechnicalDepth)
	assert.Equal(t, "casual", profile.ToneTolerance)
	assert.Equal(t, "high", profile.UrgencyBaseline)
	assert.Equal(t, "Marcus", profile.UserName)
	assert.Equal(t, "EST", profile.Timezone)
	assert.Equal(t, ColdStartComplete, csp.State())
}
