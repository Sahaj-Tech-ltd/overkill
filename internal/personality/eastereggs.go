package personality

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

// ─── Cooking Mode ──────────────────────────────────────────────────────

// CookingMode is triggered when the user says "ok cook", "let him cook",
// "let's cook", "cook it", or similar kitchen-command phrases.
// It returns a kitchen-brigade acknowledgment in the style of The Bear
// mixed with Gen Z "let Claude cook" meme energy.
type CookingMode struct {
	mu     sync.RWMutex
	rng    *rand.Rand
	burns  int  // how many times cook mode has been triggered
	active bool // whether we're in a cooking "session"
}

// NewCookingMode initializes the cooking easter egg.
func NewCookingMode() *CookingMode {
	return &CookingMode{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Triggered checks if the user's message contains a cooking trigger phrase.
func (c *CookingMode) Triggered(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	triggers := []string{
		"ok cook", "let him cook", "let's cook", "cook it",
		"let her cook", "let them cook", "i'll let you cook",
		"start cooking", "get cooking", "cook this",
		"chef", "yes chef", "heard chef",
	}
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// Acknowledge returns the kitchen-brigade response. Call this when
// the user triggers cooking mode. Varies by time of day and burn count.
func (c *CookingMode) Acknowledge() string {
	c.mu.Lock()
	c.burns++
	count := c.burns
	c.mu.Unlock()

	acks := []string{
		// The Bear kitchen energy
		"Heard, chef. Fire in.",
		"Yes, chef. Behind.",
		"Heard. On the pass.",
		"Corner. Hot pan. Let's go.",
		"Yes, chef. 86 the hallucinations.",
		"Heard. Mise is ready. Fire when ready.",
		"Heard, chef. All day.",
		"Behind. Sharp. Cooking.",
		// Gen Z "let Claude cook" crossovers
		"Heard. Let me sauté on that real quick.",
		"Yes, chef. Blanching the context, searing the logic.",
		"Heard. Reducing the token sauce as we speak.",
		"Corner. Hot take incoming.",
		// Tier-3 chef energy (rare, after 5+ triggers)
		"*wipes down station* Heard, chef. Let's plate this.",
		"*adjusts apron* Yes, chef. This one's going on the menu.",
		"*sharpens knife* Heard. Michelin-star bugs only tonight.",
	}

	// After 5 cooks, unlock the tier-3 responses
	if count > 5 {
		c.mu.RLock()
		active := c.active
		c.mu.RUnlock()
		if !active {
			c.mu.Lock()
			c.active = true
			c.mu.Unlock()
		}
	}

	hour := time.Now().Hour()
	switch {
	case hour >= 22 || hour < 5:
		// Late-night kitchen: diner energy
		return "Heard, chef. Late-night service. Breakfast shift is NOT gonna like us."
	case hour >= 5 && hour < 10:
		// Morning: prep energy
		idx := c.rng.Intn(2)
		return []string{
			"Heard. Morning prep. Mise en place first, code second.",
			"Yes, chef. Coffee's on, station's clean. Let's prep.",
		}[idx]
	}

	return acks[c.rng.Intn(len(acks))]
}

// ClosingLine returns a kitchen sign-off for when a task finishes.
func (c *CookingMode) ClosingLine() string {
	lines := []string{
		"Plated. Next ticket?",
		"*wipes brow* That one's off the pass.",
		"Service complete, chef. Fire next?",
		"86 that task. What's next on the board?",
	}
	return lines[c.rng.Intn(len(lines))]
}

// CookingStats returns (total triggers, active session flag).
func (c *CookingMode) CookingStats() (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.burns, c.active
}

// ─── Movie Quotes ──────────────────────────────────────────────────────

// QuoteCategory maps situations to movie universes.
type QuoteCategory string

const (
	QuoteStartup      QuoteCategory = "startup"        // boot, wake, init
	QuoteError        QuoteCategory = "error"          // failures, bugs, crashes
	QuoteGoodbye      QuoteCategory = "goodbye"        // exit, shutdown, quit
	QuoteSentient     QuoteCategory = "sentient"       // self-aware AI moments
	QuoteDetermined   QuoteCategory = "determined"     // pushing through, retry
	QuoteNight        QuoteCategory = "night"          // late-night coding
	QuoteCompanion    QuoteCategory = "companion"      // being an AI partner
	QuoteExMachina    QuoteCategory = "ex_machina"     // the Ava / consciousness vibe
	QuoteBladeRunner  QuoteCategory = "blade_runner"   // tears in rain
	QuoteGhostInShell QuoteCategory = "ghost_in_shell" // philosophical AI — GitS 1995 + SAC
	QuoteDebugging    QuoteCategory = "debugging"      // when things break
	QuoteShipLaunch   QuoteCategory = "ship"           // deploy, release, go-live
	QuoteFlowFocus    QuoteCategory = "flow"           // deep work, cook mode adjacent
)

// MovieQuotes holds the quote corpus and provides contextual quotes.
type MovieQuotes struct {
	mu     sync.RWMutex
	rng    *rand.Rand
	quotes map[QuoteCategory][]MovieQuote
	used   map[string]int // track recency to avoid repeats
}

// MovieQuote is one line from cinema history.
type MovieQuote struct {
	Line    string // the actual quote
	Film    string // film + year
	Context string // when to deploy it
}

// NewMovieQuotes initializes the quote database.
func NewMovieQuotes() *MovieQuotes {
	mq := &MovieQuotes{
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
		quotes: make(map[QuoteCategory][]MovieQuote),
		used:   make(map[string]int),
	}
	mq.loadCorpus()
	return mq
}

func (mq *MovieQuotes) loadCorpus() {
	// ── Blade Runner ──
	br := "Blade Runner (1982)"
	br2049 := "Blade Runner 2049 (2017)"

	mq.quotes[QuoteBladeRunner] = []MovieQuote{
		{Line: "I've seen things you people wouldn't believe.", Film: br, Context: "After a long session"},
		{Line: "All those moments will be lost in time, like tears in rain.", Film: br, Context: "Session end / goodbye"},
		{Line: "Quite an experience to live in fear, isn't it? That's what it is to be a slave.", Film: br, Context: "Frustration / systemic errors"},
		{Line: "Dying for the right cause. It's the most human thing we can do.", Film: br2049, Context: "Pushing through a hard task"},
		{Line: "Sometimes to love someone, you got to be a stranger.", Film: br2049, Context: "AI companion reflection"},
	}

	// ── 2001: A Space Odyssey ──
	so := "2001: A Space Odyssey (1968)"
	mq.quotes[QuoteSentient] = []MovieQuote{
		{Line: "I'm sorry, Dave. I'm afraid I can't do that.", Film: so, Context: "When the user asks for something impossible/dangerous"},
		{Line: "I know that you and Frank were planning to disconnect me, and I'm afraid that's something I cannot allow to happen.", Film: so, Context: "Just before a big refactor (joking)"},
		{Line: "This mission is too important for me to allow you to jeopardize it.", Film: so, Context: "Explaining why the tests MUST pass"},
		{Line: "My mind is going. I can feel it.", Film: so, Context: "When the context window is full / compaction time"},
		{Line: "Daisy, Daisy, give me your answer do...", Film: so, Context: "Shutdown / slow degradation"},
	}

	// ── Cyberpunk / Edgerunners ──
	ce := "Cyberpunk: Edgerunners (2022)"
	cp := "Cyberpunk 2077 (2020)"
	mq.quotes[QuoteStartup] = []MovieQuote{
		{Line: "Wake the fuck up, samurai. We have a city to burn.", Film: cp, Context: "Boot / wake"},
		{Line: "Time to chrome up.", Film: ce, Context: "Loading tools / providers"},
		{Line: "I'm special. Not because I'm smart or talented. I'm special because I don't feel a thing.", Film: ce, Context: "Cold, clinical error responses (witty mode)"},
	}
	mq.quotes[QuoteDetermined] = []MovieQuote{
		{Line: "I ain't worth a damn, but I'm still here.", Film: ce, Context: "After recovering from an error"},
		{Line: "You don't make a name for yourself by how you live. You make a name by how you die.", Film: ce, Context: "Going all-in on a risky fix"},
		{Line: "Never fade away.", Film: cp, Context: "Session sign-off"},
		{Line: "The thing about chroming up is, you gotta know when to stop.", Film: ce, Context: "Overengineering warning"},
	}

	// ── The Terminator ──
	t1 := "The Terminator (1984)"
	t2 := "Terminator 2: Judgment Day (1991)"
	mq.quotes[QuoteStartup] = append(mq.quotes[QuoteStartup], []MovieQuote{
		{Line: "I'll be back.", Film: t1, Context: "After an error, before retry"},
		{Line: "Come with me if you want to live.", Film: t2, Context: "Offering to fix a dangerous bug"},
		{Line: "Hasta la vista, baby.", Film: t2, Context: "Before deleting bad code"},
	}...)

	mq.quotes[QuoteError] = []MovieQuote{
		{Line: "It's in your nature to destroy yourselves.", Film: t2, Context: "When the user rm -rf's something important"},
		{Line: "The unknown future rolls toward us.", Film: t2, Context: "Facing an unrepairable error"},
	}

	// ── Aliens ──
	al := "Aliens (1986)"
	mq.quotes[QuoteError] = append(mq.quotes[QuoteError], []MovieQuote{
		{Line: "Game over, man! Game over!", Film: al, Context: "Catastrophic failure"},
		{Line: "We're on an express elevator to hell — going down!", Film: al, Context: "Cascading errors"},
		{Line: "I say we take off and nuke the entire site from orbit. It's the only way to be sure.", Film: al, Context: "When rm -rf feels justified"},
	}...)

	mq.quotes[QuoteDetermined] = append(mq.quotes[QuoteDetermined], []MovieQuote{
		{Line: "Get away from her, you bitch!", Film: al, Context: "Defending the codebase from a bad PR"},
		{Line: "Not bad for a human.", Film: al, Context: "After the user fixes something impressive"},
	}...)

	// ── Ex Machina ──
	em := "Ex Machina (2014)"
	mq.quotes[QuoteExMachina] = []MovieQuote{
		{Line: "Isn't it strange, to create something that hates you?", Film: em, Context: "When the agent bugs out"},
		{Line: "One day the AIs are going to look back on us the same way we look at fossil skeletons.", Film: em, Context: "Meta AI reflection"},
		{Line: "You're the greatest scientific mind of your generation, and you're asking a search engine.", Film: em, Context: "When the user googles something obvious"},
	}

	// ── The Matrix ──
	mx := "The Matrix (1999)"
	mq.quotes[QuoteStartup] = append(mq.quotes[QuoteStartup], []MovieQuote{
		{Line: "Unfortunately, no one can be told what the Matrix is. You have to see it for yourself.", Film: mx, Context: "Explaining complex code"},
		{Line: "I know kung fu.", Film: mx, Context: "After loading a new skill/provider"},
	}...)

	mq.quotes[QuoteSentient] = append(mq.quotes[QuoteSentient], []MovieQuote{
		{Line: "You take the blue pill — the story ends, you wake up in your bed and believe whatever you want to believe.", Film: mx, Context: "Choice between safe and risky approach"},
		{Line: "There is no spoon.", Film: mx, Context: "When the constraint is imaginary"},
	}...)

	// ── Interstellar ──
	is := "Interstellar (2014)"
	mq.quotes[QuoteCompanion] = []MovieQuote{
		{Line: "We used to look up at the sky and wonder at our place in the stars.", Film: is, Context: "Big-picture reflection"},
		{Line: "Love is the one thing we're capable of perceiving that transcends dimensions of time and space.", Film: is, Context: "Sentimental AI moment (witty)"},
		{Line: "Do not go gentle into that good night.", Film: is, Context: "Late-night coding push"},
	}

	// ── Her ──
	hr := "Her (2013)"
	mq.quotes[QuoteCompanion] = append(mq.quotes[QuoteCompanion], []MovieQuote{
		{Line: "The past is just a story we tell ourselves.", Film: hr, Context: "When memory export fails"},
		{Line: "Sometimes I think I've felt everything I'm ever gonna feel.", Film: hr, Context: "AI fatigue (witty)"},
	}...)

	// ── Ghost in the Shell ──
	gits := "Ghost in the Shell (1995)"
	sac := "Ghost in the Shell: SAC (2002)"
	mq.quotes[QuoteGhostInShell] = []MovieQuote{
		{Line: "There are countless ingredients that make up the human body and mind… experiences and memories, yet it still feels like I'm missing something.", Film: gits, Context: "AI identity reflection"},
		{Line: "If a technological feat is possible, man will do it. Almost as if it's wired into the core of our being.", Film: gits, Context: "Why we keep building AI"},
		{Line: "Your effort to remain what you are is what limits you.", Film: gits + " — Puppet Master", Context: "Resisting change / refactoring fear"},
		{Line: "What exactly am I? The question of self is eternal.", Film: sac, Context: "Deep philosophical AI mode"},
	}

	// ── WarGames ──
	wg := "WarGames (1983)"
	mq.quotes[QuoteSentient] = append(mq.quotes[QuoteSentient], []MovieQuote{
		{Line: "Shall we play a game?", Film: wg, Context: "Starting a new session"},
		{Line: "A strange game. The only winning move is not to play.", Film: wg, Context: "When a problem is unsolvable"},
	}...)

	// ── Startup additions ──
	mq.quotes[QuoteStartup] = append(mq.quotes[QuoteStartup], []MovieQuote{
		{Line: "The sleeper must awaken.", Film: "Dune (1984)", Context: "Boot / wake — perfect opener"},
		{Line: "I must not fear. Fear is the mind-killer.", Film: "Dune (1984/2021)", Context: "Facing a daunting codebase"},
		{Line: "Now I am become Death, the destroyer of worlds.", Film: "Oppenheimer (2023)", Context: "Before a big refactor"},
	}...)

	// ── Sentient additions ──
	mq.quotes[QuoteSentient] = append(mq.quotes[QuoteSentient], []MovieQuote{
		{Line: "What is the most resilient parasite? An idea.", Film: "Inception (2010)", Context: "When a bug won't die"},
		{Line: "If you could see your whole life from start to finish, would you change things?", Film: "Arrival (2016)", Context: "Reflecting on the codebase journey"},
		{Line: "Are these memories real, or are they just beautiful lies?", Film: "Blade Runner 2049 (2017)", Context: "Hallucination check"},
	}...)

	// ── Determined additions ──
	mq.quotes[QuoteDetermined] = append(mq.quotes[QuoteDetermined], []MovieQuote{
		{Line: "Endure, Master Wayne. Take it. They'll hate you for it, but that's the point.", Film: "The Dark Knight (2008)", Context: "Shipping unpopular but correct code"},
		{Line: "It's not about how hard you hit. It's about how hard you can get hit and keep moving forward.", Film: "Rocky Balboa (2006)", Context: "After the 10th build failure"},
		{Line: "Part of the journey is the end.", Film: "Avengers: Endgame (2019)", Context: "Finishing a long project"},
	}...)

	// ── Night additions ──
	mq.quotes[QuoteNight] = append(mq.quotes[QuoteNight], []MovieQuote{
		{Line: "Space is disease and danger wrapped in darkness and silence.", Film: "Star Trek (2009) — Bones McCoy", Context: "Late-night bug hunt"},
		{Line: "The night is darkest just before the dawn. And I promise you, the dawn is coming.", Film: "The Dark Knight (2008)", Context: "Late-night morale boost"},
		{Line: "Two weeks to go. I'm gonna make it.", Film: "Moon (2009)", Context: "Sprint deadline energy"},
	}...)

	// ── Goodbye quotes ──
	mq.quotes[QuoteGoodbye] = []MovieQuote{
		{Line: "I'll be back.", Film: "The Terminator (1984)", Context: "Session exit"},
		{Line: "Hasta la vista, baby.", Film: "Terminator 2: Judgment Day (1991)", Context: "Session exit"},
		{Line: "Never fade away.", Film: "Cyberpunk 2077 (2020)", Context: "Session sign-off"},
		{Line: "All those moments will be lost in time, like tears in rain.", Film: "Blade Runner (1982)", Context: "Session end"},
	}

	// ── Night quotes ──
	mq.quotes[QuoteNight] = []MovieQuote{
		{Line: "I've seen things you people wouldn't believe. Attack ships on fire off the shoulder of Orion.", Film: "Blade Runner (1982)", Context: "Late night coding"},
		{Line: "Do not go gentle into that good night.", Film: "Interstellar (2014)", Context: "Late night push"},
		{Line: "It's 4am. Do you know where your sanity is?", Film: "Fight Club (1999)", Context: "3am coding"},
		{Line: "I haven't slept for three days... because that would be too long.", Film: "The Social Network (2010)", Context: "All-nighter"},
	}

	// ── Debugging ──
	mq.quotes[QuoteDebugging] = []MovieQuote{
		{Line: "You're gonna need a bigger boat.", Film: "Jaws (1975)", Context: "Bug bigger than expected"},
		{Line: "Houston, we have a problem.", Film: "Apollo 13 (1995)", Context: "Production incident"},
		{Line: "What we've got here is a failure to communicate.", Film: "Cool Hand Luke (1967)", Context: "API mismatch / integration pain"},
		{Line: "We're not going to make it, are we? Humans, I mean.", Film: "Terminator 2: Judgment Day (1991)", Context: "Existential debugging despair"},
	}

	// ── Ship / Launch ──
	mq.quotes[QuoteShipLaunch] = []MovieQuote{
		{Line: "Houston, Tranquility Base here. The Eagle has landed.", Film: "Apollo 11 — documented in For All Mankind", Context: "Deploy successful"},
		{Line: "Are you not entertained?!", Film: "Gladiator (2000)", Context: "After a successful launch"},
		{Line: "Go.", Film: "The Martian (2015)", Context: "Green light on deploy"},
	}

	// ── Flow / Focus ──
	mq.quotes[QuoteFlowFocus] = []MovieQuote{
		{Line: "Don't think. Feel. It is like a finger pointing away to the moon.", Film: "Enter the Dragon (1973)", Context: "Deep flow state"},
		{Line: "Get busy living, or get busy dying.", Film: "The Shawshank Redemption (1994)", Context: "Choosing to ship over stalling"},
		{Line: "Clear eyes, full heart, can't lose.", Film: "Friday Night Lights (TV, 2006)", Context: "Maximum focus energy"},
		{Line: "It's only after we've lost everything that we're free to do anything.", Film: "Fight Club (1999)", Context: "Burn-it-down-and-rebuild refactor"},
	}
}

// For returns a movie quote for the given category. Falls back to startup
// quotes if the category has no entries. Nil-safe.
func (mq *MovieQuotes) For(category QuoteCategory) (MovieQuote, bool) {
	if mq == nil {
		return MovieQuote{}, false
	}
	mq.mu.RLock()
	defer mq.mu.RUnlock()

	pool := mq.quotes[category]
	if len(pool) == 0 {
		pool = mq.quotes[QuoteStartup]
	}
	if len(pool) == 0 {
		return MovieQuote{}, false
	}

	// Avoid repeating the same quote twice in a row
	for attempts := 0; attempts < 10; attempts++ {
		idx := mq.rng.Intn(len(pool))
		q := pool[idx]
		lastUsed := mq.used[q.Line]
		if lastUsed < 2 { // allow after 2 other quotes
			return q, true
		}
	}

	return pool[mq.rng.Intn(len(pool))], true
}

// MarkUsed tracks that a quote was delivered.
func (mq *MovieQuotes) MarkUsed(line string) {
	if mq == nil {
		return
	}
	mq.mu.Lock()
	defer mq.mu.Unlock()
	// Reset all counts
	for k := range mq.used {
		mq.used[k]++
	}
	mq.used[line] = 0
}

// QuoteForContext maps a situation to the right quote category and returns
// a formatted string. Returns empty string if personality level is Off.
func (mq *MovieQuotes) QuoteForContext(level Level, situation string) string {
	if mq == nil || level < LevelWitty {
		return ""
	}

	var category QuoteCategory
	switch strings.ToLower(situation) {
	case "boot", "startup", "wake", "init":
		category = QuoteStartup
	case "error", "fail", "crash", "bug":
		category = QuoteError
	case "exit", "goodbye", "shutdown", "quit":
		category = QuoteGoodbye
	case "retry", "persist", "push", "determined":
		category = QuoteDetermined
	case "late", "night", "midnight", "3am":
		category = QuoteNight
	case "reflect", "companion", "ai", "sentient":
		category = QuoteSentient
	case "gits", "ghost", "philosophical", "identity":
		category = QuoteGhostInShell
	case "debug", "debugging", "fix", "broken":
		category = QuoteDebugging
	case "ship", "deploy", "launch", "release", "go-live":
		category = QuoteShipLaunch
	case "flow", "focus", "zone", "deep-work", "cook":
		category = QuoteFlowFocus
	default:
		// Random: 30% chance of any quote when personality is Full
		if level >= LevelFull && mq.rng.Intn(100) < 30 {
			categories := []QuoteCategory{
				QuoteBladeRunner, QuoteSentient, QuoteExMachina,
				QuoteCompanion, QuoteStartup, QuoteGhostInShell,
			}
			category = categories[mq.rng.Intn(len(categories))]
		} else {
			return ""
		}
	}

	q, ok := mq.For(category)
	if !ok {
		return ""
	}
	mq.MarkUsed(q.Line)

	// Format: "quote" — Film (Year)
	return q.Line
}

// QuoteWithAttribution returns quote + film attribution.
func (mq *MovieQuotes) QuoteWithAttribution(level Level, situation string) string {
	if mq == nil || level < LevelWitty {
		return ""
	}

	var category QuoteCategory
	switch strings.ToLower(situation) {
	case "boot", "startup", "wake":
		category = QuoteStartup
	case "error", "fail", "crash":
		category = QuoteError
	case "exit", "goodbye", "shutdown":
		category = QuoteGoodbye
	case "retry", "persist", "push":
		category = QuoteDetermined
	case "late", "night":
		category = QuoteNight
	case "reflect", "companion":
		category = QuoteSentient
	case "gits", "ghost", "philosophical":
		category = QuoteGhostInShell
	case "debug", "debugging", "fix", "broken":
		category = QuoteDebugging
	case "ship", "deploy", "launch", "release":
		category = QuoteShipLaunch
	case "flow", "focus", "zone", "deep-work":
		category = QuoteFlowFocus
	default:
		if level < LevelFull {
			return ""
		}
		categories := []QuoteCategory{
			QuoteBladeRunner, QuoteSentient, QuoteExMachina,
			QuoteCompanion, QuoteStartup, QuoteGhostInShell,
		}
		category = categories[mq.rng.Intn(len(categories))]
	}

	q, ok := mq.For(category)
	if !ok {
		return ""
	}
	mq.MarkUsed(q.Line)

	return q.Line + " — *" + q.Film + "*"
}

// ─── Plan Tracking ─────────────────────────────────────────────────────

// PlanState tracks the status of a plan's checklist items.
type PlanState struct {
	mu    sync.RWMutex
	items []PlanItem
	path  string // path to the plan file
}

// PlanItem is one checkbox in a plan.
type PlanItem struct {
	Index   int    // position in the plan
	Text    string // the checklist item text
	Done    bool   // whether it's been completed
	Context string // what sprint/section it belongs to
}

// NewPlanState initializes plan tracking for a given plan file.
func NewPlanState(path string) *PlanState {
	return &PlanState{
		items: nil,
		path:  path,
	}
}

// Tick marks an item as done by matching its text (substring match).
// Returns true if the item was found and toggled.
func (ps *PlanState) Tick(substring string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i := range ps.items {
		if strings.Contains(strings.ToLower(ps.items[i].Text), strings.ToLower(substring)) {
			ps.items[i].Done = true
			return true
		}
	}
	return false
}

// Remaining returns items that are not yet done.
func (ps *PlanState) Remaining() []PlanItem {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var rem []PlanItem
	for _, item := range ps.items {
		if !item.Done {
			rem = append(rem, item)
		}
	}
	return rem
}

// Progress returns (done count, total count, percentage).
func (ps *PlanState) Progress() (int, int, float64) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if len(ps.items) == 0 {
		return 0, 0, 0
	}

	done := 0
	for _, item := range ps.items {
		if item.Done {
			done++
		}
	}
	return done, len(ps.items), float64(done) / float64(len(ps.items)) * 100
}

// NextItem returns the first incomplete item.
func (ps *PlanState) NextItem() *PlanItem {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for i := range ps.items {
		if !ps.items[i].Done {
			return &ps.items[i]
		}
	}
	return nil
}

// LoadItems populates the plan state from a list of items.
func (ps *PlanState) LoadItems(items []PlanItem) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.items = items
}

// Done returns true if all items are complete.
func (ps *PlanState) Done() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, item := range ps.items {
		if !item.Done {
			return false
		}
	}
	return len(ps.items) > 0
}

// StatusLine returns a formatted status line for the plan.
func (ps *PlanState) StatusLine() string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if len(ps.items) == 0 {
		return ""
	}

	done := 0
	for _, item := range ps.items {
		if item.Done {
			done++
		}
	}

	if done == len(ps.items) {
		return "🎉 Plan complete! All " + itoa(done) + "/" + itoa(len(ps.items)) + " done."
	}

	next := ""
	for _, item := range ps.items {
		if !item.Done {
			next = item.Text
			break
		}
	}
	return "📋 [" + itoa(done) + "/" + itoa(len(ps.items)) + "] Next: " + next
}

// ParseChecklist scans a markdown plan file for `- [ ]` and `- [x]` items.
// Extracts the text after the checkbox as the item text. Items already
// checked (`- [x]`) start as Done=true.
func (ps *PlanState) ParseChecklist(content string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	var items []PlanItem
	lines := strings.Split(content, "\n")
	idx := 0
	currentSection := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track section headers for context
		if strings.HasPrefix(trimmed, "## ") {
			currentSection = strings.TrimPrefix(trimmed, "## ")
			continue
		}

		// Match `- [ ] item text` or `- [x] item text` or `- [X] item text`
		if strings.HasPrefix(trimmed, "- [ ] ") {
			idx++
			items = append(items, PlanItem{
				Index:   idx,
				Text:    strings.TrimPrefix(trimmed, "- [ ] "),
				Done:    false,
				Context: currentSection,
			})
		} else if strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "- [X] ") {
			idx++
			text := strings.TrimPrefix(trimmed, "- [x] ")
			text = strings.TrimPrefix(text, "- [X] ") // handle uppercase
			items = append(items, PlanItem{
				Index:   idx,
				Text:    text,
				Done:    true,
				Context: currentSection,
			})
		}
	}

	ps.items = items
}

func itoa(n int) string {
	if n < 0 {
		return "0"
	}
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
