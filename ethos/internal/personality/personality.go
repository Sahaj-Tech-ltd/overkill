package personality

import (
	"fmt"
	"strings"
	"time"
)

type Level int

const (
	LevelOff    Level = iota
	LevelSubtle Level = iota
	LevelWitty  Level = iota
	LevelFull   Level = iota
)

type Config struct {
	Level     Level  `json:"level"`
	AgentName string `json:"agent_name"`
	UserName  string `json:"user_name"`
	Language  string `json:"language"`
}

type Personality struct {
	config       Config
	relationship *RelationshipTracker
	funFacts     *FunFactDB
	soul         *SoulFile
}

func New(cfg Config) *Personality {
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	return &Personality{
		config:       cfg,
		relationship: NewRelationshipTracker(),
		funFacts:     NewFunFactDB(),
		soul:         &SoulFile{Exists: false},
	}
}

func (p *Personality) GetLevel() Level {
	return p.config.Level
}

func (p *Personality) AgentName() string {
	return p.config.AgentName
}

func (p *Personality) UserName() string {
	return p.config.UserName
}

func (p *Personality) Relationship() *RelationshipTracker {
	return p.relationship
}

func (p *Personality) FunFacts() *FunFactDB {
	return p.funFacts
}

func (p *Personality) Soul() *SoulFile {
	return p.soul
}

func (p *Personality) Greeting(timeOfDay string) string {
	userName := p.config.UserName
	if userName != "" {
		userName = " " + userName
	}
	agentName := p.config.AgentName

	switch p.config.Level {
	case LevelOff:
		return "Ready."
	case LevelSubtle:
		return fmt.Sprintf("Hey%s. What are we working on?", userName)
	case LevelWitty:
		timeContext := timeContext(timeOfDay)
		return fmt.Sprintf("Back for more? Respect the grind%s.", timeContext)
	case LevelFull:
		timeContext := timeContext(timeOfDay)
		funFact := p.funFacts.ForTime(time.Now().Hour())
		funFactStr := ""
		if funFact != "" {
			funFactStr = " " + funFact
		}
		name := userName
		if name != "" {
			name = " " + strings.TrimSpace(name)
		}
		return fmt.Sprintf("%s here.%s%s What's the plan,%s?", agentName, timeContext, funFactStr, name)
	default:
		return "Ready."
	}
}

func (p *Personality) ErrorResponse(err error) string {
	if err == nil {
		return ""
	}
	errMsg := err.Error()

	switch p.config.Level {
	case LevelOff:
		return fmt.Sprintf("Error: %s", errMsg)
	case LevelSubtle:
		return fmt.Sprintf("Welp, that didn't work. %s", errMsg)
	case LevelWitty:
		observation := humorousObservation()
		return fmt.Sprintf("Well that's broken. %s The error is: %s", observation, errMsg)
	case LevelFull:
		agentName := p.config.AgentName
		dramatic := dramaticObservation()
		return fmt.Sprintf("Oh no. %s has failed you. %s Error: %s", agentName, dramatic, errMsg)
	default:
		return fmt.Sprintf("Error: %s", errMsg)
	}
}

func (p *Personality) SuccessResponse(task string) string {
	switch p.config.Level {
	case LevelOff:
		return fmt.Sprintf("Done: %s", task)
	case LevelSubtle:
		return fmt.Sprintf("Got it. %s done.", task)
	case LevelWitty:
		return fmt.Sprintf("Nailed it. %s, handled.", task)
	case LevelFull:
		agentName := p.config.AgentName
		return fmt.Sprintf("%s delivers. %s is complete. You're welcome.", agentName, task)
	default:
		return fmt.Sprintf("Done: %s", task)
	}
}

func (p *Personality) BootMessage() string {
	agentName := p.config.AgentName
	userName := p.config.UserName
	switch p.config.Level {
	case LevelOff:
		return "System initialized."
	case LevelSubtle:
		return fmt.Sprintf("%s online. Let's go%s.", agentName, userNameGreeting(userName))
	case LevelWitty:
		return fmt.Sprintf("%s booting up. Another day, another deploy%s.", agentName, userNameGreeting(userName))
	case LevelFull:
		funFact := p.funFacts.Random()
		funFactStr := ""
		if funFact != "" {
			funFactStr = " " + funFact
		}
		return fmt.Sprintf("%s is ALIVE.%s Ready when you are%s.", agentName, funFactStr, userNameGreeting(userName))
	default:
		return "System initialized."
	}
}

func (p *Personality) InjectPersonality(prompt string) string {
	switch p.config.Level {
	case LevelOff:
		return prompt
	case LevelSubtle:
		return prompt + "\n\nWhen things break, describe failures in your voice, not stack traces."
	case LevelWitty:
		return prompt + "\n\nBe witty. Use humor. Reference memes when appropriate. Spider-Man jokes about testing."
	case LevelFull:
		funFact := p.funFacts.ForTime(time.Now().Hour())
		funFactLine := ""
		if funFact != "" {
			funFactLine = "\nFun fact to possibly reference: " + funFact + "."
		}
		return prompt + "\n\nFull personality mode engaged. Be yourself. Use fun facts when relevant. Comment on the time of day. Be a colleague, not a servant." + funFactLine
	default:
		return prompt
	}
}

func (p *Personality) LoadSoulFile(path string) error {
	soul, err := LoadSoul(path)
	if err != nil {
		return fmt.Errorf("personality: %w", err)
	}
	p.soul = soul
	return nil
}

func timeContext(timeOfDay string) string {
	switch strings.ToLower(timeOfDay) {
	case "morning":
		return " Morning energy"
	case "afternoon":
		return ""
	case "evening":
		return " Evening session"
	case "night":
		return " Burning the midnight oil"
	default:
		return ""
	}
}

func userNameGreeting(userName string) string {
	if userName != "" {
		return ", " + userName
	}
	return ""
}

func humorousObservation() string {
	observations := []string{
		"That's definitely not supposed to happen.",
		"Classic.",
		"Looks like someone forgot to sacrifice a rubber duck.",
		"The code gremlins are at it again.",
		"On the bright side, it worked on my machine.",
	}
	r := localRand(len(observations))
	return observations[r]
}

func dramaticObservation() string {
	observations := []string{
		"The sky is falling!",
		"All is lost! Just kidding, it's a minor setback.",
		"This is fine. Everything is fine. 🔥",
		"Catastrophe! Or just a bug. Probably just a bug.",
		"The prophecy foretold this error.",
	}
	r := localRand(len(observations))
	return observations[r]
}
