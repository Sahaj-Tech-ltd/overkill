package personality

import (
	"math/rand"
	"sync"
	"time"
)

type FunFact struct {
	Fact      string `json:"fact"`
	Category  string `json:"category"`
	TimeSlots []int  `json:"time_slots"`
}

type FunFactDB struct {
	mu    sync.RWMutex
	facts []FunFact
	used  map[int]bool
	rnd   *rand.Rand
}

func NewFunFactDB() *FunFactDB {
	return &FunFactDB{
		facts: defaultFacts(),
		used:  make(map[int]bool),
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (db *FunFactDB) Random() string {
	db.mu.Lock()
	defer db.mu.Unlock()

	if len(db.facts) == 0 {
		return ""
	}

	idx := db.rnd.Intn(len(db.facts))
	return db.facts[idx].Fact
}

func (db *FunFactDB) ForTime(hour int) string {
	db.mu.Lock()
	defer db.mu.Unlock()

	var matching []int
	for i, f := range db.facts {
		if len(f.TimeSlots) == 0 {
			continue
		}
		for _, h := range f.TimeSlots {
			if h == hour {
				matching = append(matching, i)
				break
			}
		}
	}

	if len(matching) == 0 {
		if len(db.facts) == 0 {
			return ""
		}
		idx := db.rnd.Intn(len(db.facts))
		return db.facts[idx].Fact
	}

	idx := db.rnd.Intn(len(matching))
	return db.facts[matching[idx]].Fact
}

func (db *FunFactDB) ForContext(context string) string {
	db.mu.Lock()
	defer db.mu.Unlock()

	var matching []int
	for i, f := range db.facts {
		if f.Category == context {
			matching = append(matching, i)
		}
	}

	if len(matching) == 0 {
		if len(db.facts) == 0 {
			return ""
		}
		idx := db.rnd.Intn(len(db.facts))
		return db.facts[idx].Fact
	}

	idx := db.rnd.Intn(len(matching))
	return db.facts[matching[idx]].Fact
}

func (db *FunFactDB) Count() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.facts)
}

func defaultFacts() []FunFact {
	return []FunFact{
		{
			Fact:      "Did you know lemon juice sanitizes a cutting board better than soap?",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "The first computer bug was an actual bug. A moth found in a Harvard Mark II in 1947.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "After 17 hours awake, your cognitive function matches a 0.05% BAC.",
			Category:  "sleep",
			TimeSlots: []int{0, 1, 2, 3, 4, 5, 22, 23},
		},
		{
			Fact:      "The word 'Monday' comes from Old English 'Mōnandæg' — Moon's day.",
			Category:  "monday",
			TimeSlots: []int{8, 9, 10},
		},
		{
			Fact:      "Rubber duck debugging is real. A study found verbalizing problems improves solution rates by 30%.",
			Category:  "debug",
			TimeSlots: nil,
		},
		{
			Fact:      "Only 1% of people are true night owls. The rest are just procrastinating.",
			Category:  "late",
			TimeSlots: []int{0, 1, 2, 3, 4},
		},
		{
			Fact:      "Honey never spoils. Archaeologists found 3,000-year-old honey in Egyptian tombs that was still edible.",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "The average programmer writes 10 lines of production code per day. The rest is thinking, reading, and debugging.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "Octopuses have three hearts and blue blood. Two hearts pump blood to the gills, one to the body.",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "The term 'debugging' was popularized by Grace Hopper, who literally debugged a computer by removing a moth.",
			Category:  "debug",
			TimeSlots: nil,
		},
		{
			Fact:      "A jiffy is an actual unit of time: 1/100th of a second.",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "The first website ever created is still online: info.cern.ch, created by Tim Berners-Lee in 1991.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "Your brain uses about 20% of your body's total energy, despite being only 2% of your body weight.",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "The QWERTY keyboard was designed to prevent typewriter jams by separating commonly used letter pairs.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "A group of flamingos is called a 'flamboyance.' Nature has a sense of humor.",
			Category:  "general",
			TimeSlots: nil,
		},
		{
			Fact:      "Sleep deprivation can cause your brain to momentarily shut down for a few seconds without you noticing.",
			Category:  "sleep",
			TimeSlots: []int{0, 1, 2, 3, 4, 5, 22, 23},
		},
		{
			Fact:      "The average bug fix takes 6 attempts. If you got it in 3, you're ahead of the curve.",
			Category:  "debug",
			TimeSlots: nil,
		},
		{
			Fact:      "There are approximately 700 programming languages. You only need to know 3 to be dangerous.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "Caffeine takes about 20 minutes to kick in. That quick coffee isn't as quick as you think.",
			Category:  "general",
			TimeSlots: []int{7, 8, 9, 13, 14},
		},
		{
			Fact:      "The first ever 1GB hard drive, announced in 1980, weighed about 550 pounds and cost $40,000.",
			Category:  "coding",
			TimeSlots: nil,
		},
		{
			Fact:      "Tuesday is the most productive day of the week. Wednesday is when most people give up.",
			Category:  "monday",
			TimeSlots: []int{8, 9, 10},
		},
		{
			Fact:      "A study found that developers spend roughly 75% of their time understanding existing code, not writing new code.",
			Category:  "coding",
			TimeSlots: nil,
		},
	}
}
