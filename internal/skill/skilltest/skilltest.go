package skilltest

import "github.com/Temikus/denkeeper/internal/skill"

// New constructs a Skill with parsed triggers for use in tests.
// Panics if any trigger string is invalid.
func New(name, description string, triggers []string, body string) skill.Skill {
	parsed, err := skill.ParseTriggers(triggers)
	if err != nil {
		panic("skilltest.New: " + err.Error())
	}
	return skill.Skill{
		Name:           name,
		Description:    description,
		Triggers:       triggers,
		ParsedTriggers: parsed,
		Body:           body,
	}
}
