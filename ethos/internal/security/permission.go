package security

import (
	"sync"
	"time"
)

type PermissionAction int

const (
	ActionDeny PermissionAction = iota
	ActionAllowOnce
	ActionAllowProject
	ActionAllowGlobal
)

func (a PermissionAction) String() string {
	switch a {
	case ActionDeny:
		return "deny"
	case ActionAllowOnce:
		return "allow_once"
	case ActionAllowProject:
		return "allow_project"
	case ActionAllowGlobal:
		return "allow_global"
	default:
		return "unknown"
	}
}

type PermissionDecision struct {
	Action      PermissionAction
	Pattern     string
	Command     string
	ProjectPath string
	ExpiresAt   time.Time
}

type PermissionManager struct {
	onceAllowed    map[string]bool
	projectAllowed map[string]map[string]bool
	globalAllowed  map[string]bool
	mu             sync.RWMutex
}

func NewPermissionManager() *PermissionManager {
	return &PermissionManager{
		onceAllowed:    make(map[string]bool),
		projectAllowed: make(map[string]map[string]bool),
		globalAllowed:  make(map[string]bool),
	}
}

func (pm *PermissionManager) Check(pattern, command, projectPath string) PermissionDecision {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.onceAllowed[pattern] {
		return PermissionDecision{
			Action:      ActionAllowOnce,
			Pattern:     pattern,
			Command:     command,
			ProjectPath: projectPath,
		}
	}

	if projects, ok := pm.projectAllowed[projectPath]; ok && projects[pattern] {
		return PermissionDecision{
			Action:      ActionAllowProject,
			Pattern:     pattern,
			Command:     command,
			ProjectPath: projectPath,
		}
	}

	if pm.globalAllowed[pattern] {
		return PermissionDecision{
			Action:      ActionAllowGlobal,
			Pattern:     pattern,
			Command:     command,
			ProjectPath: projectPath,
		}
	}

	return PermissionDecision{
		Action:      ActionDeny,
		Pattern:     pattern,
		Command:     command,
		ProjectPath: projectPath,
	}
}

func (pm *PermissionManager) Allow(decision PermissionDecision) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	switch decision.Action {
	case ActionAllowOnce:
		pm.onceAllowed[decision.Pattern] = true
	case ActionAllowProject:
		if pm.projectAllowed[decision.ProjectPath] == nil {
			pm.projectAllowed[decision.ProjectPath] = make(map[string]bool)
		}
		pm.projectAllowed[decision.ProjectPath][decision.Pattern] = true
	case ActionAllowGlobal:
		pm.globalAllowed[decision.Pattern] = true
	}
}

func (pm *PermissionManager) IsAllowed(pattern, command, projectPath string) bool {
	decision := pm.Check(pattern, command, projectPath)
	return decision.Action != ActionDeny
}

func (pm *PermissionManager) ClearProject(projectPath string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.projectAllowed, projectPath)
}

func (pm *PermissionManager) ClearSession() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.onceAllowed = make(map[string]bool)
}
