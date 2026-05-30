// Package personality — contextual fun fact database with time-of-day
// and random fact retrieval.
package personality

import (
	"math/rand"
	"sync"
	"time"
)

// FunFact represents one contextual trivia item.
type FunFact struct {
	Category string // "sleep", "coding", "debug", "random", etc.
	Fact     string // the actual fact text
}

// FunFactDB holds the fun fact corpus and provides contextual lookup.
type FunFactDB struct {
	mu    sync.RWMutex
	facts []FunFact
	rng   *rand.Rand
}

// NewFunFactDB creates a DB pre-populated with the built-in fun fact corpus.
func NewFunFactDB() *FunFactDB {
	db := &FunFactDB{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	db.facts = defaultFunFacts()
	return db
}

// Count returns the total number of facts in the database.
func (db *FunFactDB) Count() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.facts)
}

// Random returns a random fun fact from any category.
func (db *FunFactDB) Random() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if len(db.facts) == 0 {
		return ""
	}
	return db.facts[db.rng.Intn(len(db.facts))].Fact
}

// ForTime returns a fun fact appropriate for the given hour (0-23).
func (db *FunFactDB) ForTime(hour int) string {
	switch {
	case hour >= 0 && hour < 6:
		return db.ForContext("sleep")
	case hour >= 6 && hour < 12:
		return db.ForContext("coding")
	case hour >= 12 && hour < 18:
		return db.ForContext("debug")
	default:
		return db.ForContext("coding")
	}
}

// ForContext returns a fun fact matching the given category. Falls back
// to a random fact if no match exists.
func (db *FunFactDB) ForContext(category string) string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if len(db.facts) == 0 {
		return ""
	}

	// Collect matches
	var matches []string
	for _, f := range db.facts {
		if f.Category == category {
			matches = append(matches, f.Fact)
		}
	}

	if len(matches) > 0 {
		return matches[db.rng.Intn(len(matches))]
	}

	// Fallback to random
	return db.facts[db.rng.Intn(len(db.facts))].Fact
}

func defaultFunFacts() []FunFact {
	return []FunFact{
		// General
		{Category: "general", Fact: "Fun fact: `sudo` stands for 'superuser do'. It was originally called 'substitute user do' but nobody remembers that."},
		{Category: "general", Fact: "JSON stands for JavaScript Object Notation. It's the only JavaScript thing that Python, Go, Rust, and Java developers agree on."},
		{Category: "general", Fact: "Fun fact: Docker's whale mascot is named 'Moby Dock'. Yes, really."},

		// Sleep
		{Category: "sleep", Fact: "Fun fact: sleep deprivation impairs coding judgment about as much as being legally drunk. Just saying."},
		{Category: "sleep", Fact: "3am code either ships the product or deletes the repo. There's no middle ground."},

		// Late
		{Category: "late", Fact: "The most productive commit in history was at 4:02am by a developer who doesn't remember writing it."},
		{Category: "late", Fact: "Late-night coding: where 'git commit -m stuff' counts as documentation."},

		// Coding
		{Category: "coding", Fact: "Did you know: C was originally developed to write the Unix operating system. They succeeded so hard it's still running everything."},
		{Category: "coding", Fact: "The first commit to the Linux kernel was 'This is a minix-like operating system'. Humble beginnings."},
		{Category: "coding", Fact: "Fun fact: git was written by Linus Torvalds in 10 days. He named it after himself (British slang for 'unpleasant person')."},
		{Category: "coding", Fact: "Every great codebase has a `// TODO: fix this` from 2018 that nobody has touched since."},
		{Category: "coding", Fact: "Did you know: `rm -rf /` will not work on modern systems because GNU coreutils added a `--preserve-root` safeguard. Someone learned the hard way."},

		// Monday
		{Category: "monday", Fact: "Monday commits have the highest revert rate. Tuesday commits are the cleanest. Nobody ships on Friday."},
		{Category: "monday", Fact: "Monday code has 23% more bugs. Correlation or causation? The world may never know."},
		{Category: "monday", Fact: "Shipping on Friday is how you guarantee weekend pager duty. Your future self will thank you for waiting."},

		// Debug
		{Category: "debug", Fact: "Fun fact: the term 'debugging' comes from an actual moth found in a Harvard Mark II relay in 1947. Grace Hopper taped it to the logbook."},
		{Category: "debug", Fact: "The average bug takes 17 minutes to fix and 3 hours to understand. You're above average."},
		{Category: "debug", Fact: "Rubber duck debugging is real. Naming the duck increases debugging speed by 20%."},
		{Category: "debug", Fact: "'It works on my machine' — the four most dangerous words in software."},
		{Category: "debug", Fact: "A bug is just an undocumented feature with an attitude problem."},

		// Random
		{Category: "random", Fact: "The first computer virus was called 'Creeper' and it just printed 'I'M THE CREEPER, CATCH ME IF YOU CAN'."},
		{Category: "random", Fact: "The Apollo 11 guidance computer had 72KB of memory and ran at 0.043 MHz. Your linter uses more resources."},
		{Category: "random", Fact: "Thomas Edison: 'I have not failed. I've just found 10,000 ways that won't work.' He was probably debugging."},
	}
}
