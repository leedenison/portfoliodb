package allowlist

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Mode is the allowlist matching mode.
type Mode int

const (
	ModeGlob Mode = iota
	ModeRegex
)

// Matcher matches an email against configured patterns.
type Matcher struct {
	patterns       []string
	mode           Mode
	caseSensitive  bool
	compiledRegexp []*regexp.Regexp
}

// NewMatcher builds a Matcher from patterns. For regex mode, patterns are compiled.
func NewMatcher(patterns []string, mode Mode, caseSensitive bool) (*Matcher, error) {
	m := &Matcher{
		patterns:      patterns,
		mode:          mode,
		caseSensitive: caseSensitive,
	}
	if mode == ModeRegex {
		m.compiledRegexp = make([]*regexp.Regexp, 0, len(patterns))
		for _, p := range patterns {
			if p == "" {
				continue
			}
			r, err := regexp.Compile(p)
			if err != nil {
				return nil, err
			}
			m.compiledRegexp = append(m.compiledRegexp, r)
		}
	}
	return m, nil
}

// Match returns true if email matches any pattern.
func (m *Matcher) Match(email string) bool {
	if email == "" {
		return false
	}
	subject := email
	if !m.caseSensitive {
		subject = strings.ToLower(email)
	}
	switch m.mode {
	case ModeGlob:
		for _, p := range m.patterns {
			if p == "" {
				continue
			}
			pattern := p
			if !m.caseSensitive {
				pattern = strings.ToLower(pattern)
			}
			ok, _ := filepath.Match(pattern, subject)
			if ok {
				return true
			}
		}
		return false
	case ModeRegex:
		for _, r := range m.compiledRegexp {
			if r.MatchString(subject) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
