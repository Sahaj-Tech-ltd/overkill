package tui

import (
	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/cost"
	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/journal"
	"github.com/Sahaj-Tech-ltd/ethos/internal/routing"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

type App struct {
	Agent   *agent.Agent
	Store   session.Store
	Router  routing.Router
	Costs   cost.Tracker
	Hooks   *hooks.Registry
	Config  *config.Config
	Journal *journal.FlightRecorder
}
