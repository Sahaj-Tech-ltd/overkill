package skills

import (
	"regexp"
	"strings"
)

var (
	nameRegex   = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

func Validate(skill *Skill) []ValidationError {
	var errs []ValidationError

	if strings.TrimSpace(skill.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "name is required"})
	} else if len(skill.Name) < 2 || len(skill.Name) > 50 {
		errs = append(errs, ValidationError{Field: "name", Message: "name must be 2-50 characters"})
	} else if strings.Contains(skill.Name, " ") {
		errs = append(errs, ValidationError{Field: "name", Message: "name must not contain spaces"})
	} else if !nameRegex.MatchString(skill.Name) {
		errs = append(errs, ValidationError{Field: "name", Message: "name must be lowercase letters, digits, and hyphens only"})
	}

	if strings.TrimSpace(skill.Description) == "" {
		errs = append(errs, ValidationError{Field: "description", Message: "description is required"})
	} else if len(skill.Description) < 10 || len(skill.Description) > 500 {
		errs = append(errs, ValidationError{Field: "description", Message: "description must be 10-500 characters"})
	}

	if skill.Version != "" && !semverRegex.MatchString(skill.Version) {
		errs = append(errs, ValidationError{Field: "version", Message: "version must be semver format (x.y.z)"})
	}

	for _, t := range skill.Triggers {
		if len(t) < 1 || len(t) > 50 {
			errs = append(errs, ValidationError{Field: "triggers", Message: "each trigger must be 1-50 characters"})
			break
		}
	}

	if strings.TrimSpace(skill.Instructions) == "" {
		errs = append(errs, ValidationError{Field: "instructions", Message: "instructions is required"})
	} else if len(strings.TrimSpace(skill.Instructions)) < 20 {
		errs = append(errs, ValidationError{Field: "instructions", Message: "instructions must be at least 20 characters"})
	}

	return errs
}
