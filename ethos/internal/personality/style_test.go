package personality

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestObserve_ShortMessages_InfersDirect(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("fix bug")
	si.Observe("add test")
	si.Observe("remove log")

	assert.Equal(t, CommDirect, si.Current().Communication)
}

func TestObserve_Questions_InfersCritique(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("what is the best approach for this?")

	assert.Equal(t, RespCritique, si.Current().ResponseExpect)
}

func TestObserve_Imperatives_InfersAction(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("fix the bug")

	assert.Equal(t, RespAction, si.Current().ResponseExpect)
}

func TestBaseline_DoesNotUpdateAfter1Session(t *testing.T) {
	si := NewStyleInferencer()
	original := si.Baseline()

	si.Observe("what should I do here?")
	si.CommitSession()

	assert.Equal(t, original.Communication, si.Baseline().Communication)
	assert.Equal(t, original.ResponseExpect, si.Baseline().ResponseExpect)
}

func TestBaseline_UpdatesAfterNConsistentSessions(t *testing.T) {
	si := NewStyleInferencer()

	for i := 0; i < 5; i++ {
		si.Observe("fix the bug in the handler")
		si.CommitSession()
	}

	assert.Equal(t, RespAction, si.Baseline().ResponseExpect)
}

func TestShortTerm_UpdatesImmediately(t *testing.T) {
	si := NewStyleInferencer()
	assert.Equal(t, CommDirect, si.Current().Communication)

	si.Observe("this is a really long message with lots of words and context and explanation because the reason is clear")
	assert.NotEqual(t, CommDirect, si.Current().Communication)
}

func TestDomainTerms_Accumulates(t *testing.T) {
	si := NewStyleInferencer()

	term := "kubernetes"
	for i := 0; i < 3; i++ {
		si.Observe("deploy to kubernetes cluster")
	}

	found := false
	for _, dt := range si.Current().DomainTerms {
		if dt == term {
			found = true
			break
		}
	}
	assert.True(t, found, "expected %s in domain terms, got %v", term, si.Current().DomainTerms)
}

func TestObserve_PlanWords_InfersPlansFirst(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("first do X then do Y")

	assert.Equal(t, ApproachPlansFirst, si.Current().Approach)
}

func TestFrustrationTrigger_Detected(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("why is this still broken!")

	assert.NotEmpty(t, si.Current().FrustrationTrigger)
}

func TestSetBaseline_OverridesBaseline(t *testing.T) {
	si := NewStyleInferencer()

	custom := &WorkingStyle{
		Communication:      CommVerbose,
		ResponseExpect:     RespCritique,
		FrustrationTrigger: "again",
		Approach:           ApproachPlansFirst,
		DomainTerms:        []string{"kubernetes", "terraform"},
	}
	si.SetBaseline(custom)

	assert.Equal(t, CommVerbose, si.Baseline().Communication)
	assert.Equal(t, RespCritique, si.Baseline().ResponseExpect)
	assert.Equal(t, "again", si.Baseline().FrustrationTrigger)
	assert.Equal(t, ApproachPlansFirst, si.Baseline().Approach)
	assert.Equal(t, []string{"kubernetes", "terraform"}, si.Baseline().DomainTerms)
}

func TestSetBaseline_IsolatesMutation(t *testing.T) {
	si := NewStyleInferencer()
	terms := []string{"alpha", "beta"}

	si.SetBaseline(&WorkingStyle{
		Communication: CommDirect,
		DomainTerms:   terms,
	})

	terms[0] = "modified"
	assert.Equal(t, "alpha", si.Baseline().DomainTerms[0], "SetBaseline should copy DomainTerms")
}

func TestShouldUpdateBaseline_FalseInitially(t *testing.T) {
	si := NewStyleInferencer()
	assert.False(t, si.ShouldUpdateBaseline())
}

func TestShouldUpdateBaseline_TrueAfterNSessions(t *testing.T) {
	si := NewStyleInferencer()
	for i := 0; i < 5; i++ {
		si.Observe("do something")
		si.CommitSession()
	}
	baseline := si.Baseline()
	assert.NotNil(t, baseline)
	assert.Equal(t, CommDirect, baseline.Communication)
}

func TestObserve_VerboseMessages_InfersVerbose(t *testing.T) {
	si := NewStyleInferencer()

	longMsg := strings.Repeat("word ", 35)
	si.Observe(longMsg)

	assert.Equal(t, CommVerbose, si.Current().Communication)
}

func TestObserve_ContextualExplanation(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("I need this feature because the system requires proper authentication flow")

	assert.Equal(t, CommContextual, si.Current().Communication)
}

func TestObserve_MultipleImperatives(t *testing.T) {
	si := NewStyleInferencer()

	imperatives := []string{"fix this", "add that", "remove old", "create new", "update old"}
	for _, imp := range imperatives {
		si.Observe(imp)
	}

	assert.Equal(t, RespAction, si.Current().ResponseExpect)
	assert.Equal(t, CommDirect, si.Current().Communication)
}

func TestDomainTerms_RequiresThreeOccurrences(t *testing.T) {
	si := NewStyleInferencer()

	si.Observe("deploy to kubernetes cluster")
	si.Observe("deploy to kubernetes cluster")

	found := false
	for _, dt := range si.Current().DomainTerms {
		if dt == "kubernetes" {
			found = true
		}
	}
	assert.False(t, found, "term should not appear after only 2 observations")

	si.Observe("deploy to kubernetes cluster")

	found = false
	for _, dt := range si.Current().DomainTerms {
		if dt == "kubernetes" {
			found = true
		}
	}
	assert.True(t, found, "term should appear after 3 observations")
}

func TestCommitSession_ResetsCountAfterBaseline(t *testing.T) {
	si := NewStyleInferencer()

	for i := 0; i < 5; i++ {
		si.Observe("fix the broken test")
		si.CommitSession()
	}

	assert.Equal(t, 0, si.sessionCount, "sessionCount should reset after baseline update")
	assert.Equal(t, RespAction, si.Baseline().ResponseExpect)
}

func TestObserve_QuestionOverridesImperative(t *testing.T) {
	si := NewStyleInferencer()
	si.Observe("fix this?")

	assert.Equal(t, RespCritique, si.Current().ResponseExpect)
}
