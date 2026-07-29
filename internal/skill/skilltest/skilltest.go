// Package skilltest provides constructors for building skill.Skill values in
// tests. It lives in its own package (rather than a _test.go file) so that
// tests in other packages can use it, and because nothing in the production
// build graph imports it, it is never linked into the shipped binary.
//
// Always prefer these constructors over a bare skill.Skill{} literal when the
// skill declares triggers: skill.MatchSkills keys off ParsedTriggers, so a
// literal that sets Triggers but leaves ParsedTriggers nil is treated as
// "no triggers" and silently matches every message.
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

// NewVersioned is New with an explicit Version, for tests that assert on the
// version field. Panics if any trigger string is invalid.
func NewVersioned(name, description, version string, triggers []string, body string) skill.Skill {
	s := New(name, description, triggers, body)
	s.Version = version
	return s
}
