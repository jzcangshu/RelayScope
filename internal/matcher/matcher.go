package matcher

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Rule struct {
	ID            int64    `json:"id"`
	Provider      string   `json:"provider"`
	CanonicalName string   `json:"canonicalName"`
	RequiredTerms []string `json:"requiredTerms"`
	AnyTerms      []string `json:"anyTerms"`
	ExcludedTerms []string `json:"excludedTerms"`
	Aliases       []string `json:"aliases"`
	Pattern       string   `json:"pattern"`
	Priority      int      `json:"priority"`
	Enabled       bool     `json:"enabled"`
	Generated     bool     `json:"generated"`
}

type Match struct {
	Rule        Rule
	Primary     bool
	Explanation string
}

type Preview struct {
	RawName    string
	Normalized string
	Matches    []Match
	Ambiguous  bool
}

type Engine struct {
	rules []compiledRule
}

type compiledRule struct {
	rule          Rule
	pattern       *regexp.Regexp
	requiredTerms []string
	anyTerms      []string
	excludedTerms []string
}

func New(rules []Rule) (*Engine, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.CanonicalName) == "" {
			return nil, fmt.Errorf("rule canonical name is required")
		}
		entry := compiledRule{
			rule:          rule,
			requiredTerms: normalizeTerms(rule.RequiredTerms),
			anyTerms:      normalizeTerms(append(append([]string{}, rule.AnyTerms...), rule.Aliases...)),
			excludedTerms: normalizeTerms(rule.ExcludedTerms),
		}
		if rule.Pattern != "" {
			pattern, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, fmt.Errorf("compile rule %q pattern: %w", rule.CanonicalName, err)
			}
			entry.pattern = pattern
		}
		compiled = append(compiled, entry)
	}
	return &Engine{rules: compiled}, nil
}

func (engine *Engine) Preview(rawName string) Preview {
	normalized := Normalize(rawName)
	preview := Preview{RawName: rawName, Normalized: normalized}
	for _, compiled := range engine.rules {
		if !compiled.rule.Enabled {
			continue
		}
		if matched, explanation := matchRule(compiled, rawName, normalized); matched {
			preview.Matches = append(preview.Matches, Match{Rule: compiled.rule, Explanation: explanation})
		}
	}
	sort.SliceStable(preview.Matches, func(left, right int) bool {
		if preview.Matches[left].Rule.Priority != preview.Matches[right].Rule.Priority {
			return preview.Matches[left].Rule.Priority > preview.Matches[right].Rule.Priority
		}
		return preview.Matches[left].Rule.CanonicalName < preview.Matches[right].Rule.CanonicalName
	})
	if len(preview.Matches) == 1 {
		preview.Matches[0].Primary = true
	} else if len(preview.Matches) > 1 {
		preview.Ambiguous = true
	}
	return preview
}

func Normalize(value string) string {
	var builder strings.Builder
	spacePending := false
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			if spacePending && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteRune(character)
			spacePending = false
			continue
		}
		spacePending = builder.Len() > 0
	}
	return strings.TrimSpace(builder.String())
}

func matchRule(compiled compiledRule, rawName, normalized string) (bool, string) {
	matchedRequired := make([]string, 0, len(compiled.requiredTerms))
	for _, term := range compiled.requiredTerms {
		if !containsTerm(normalized, term) {
			return false, ""
		}
		matchedRequired = append(matchedRequired, term)
	}
	if len(compiled.anyTerms) > 0 {
		matchedAny := ""
		for _, term := range compiled.anyTerms {
			if containsTerm(normalized, term) {
				matchedAny = term
				break
			}
		}
		if matchedAny == "" {
			return false, ""
		}
		matchedRequired = append(matchedRequired, "any:"+matchedAny)
	}
	for _, term := range compiled.excludedTerms {
		if containsTerm(normalized, term) {
			return false, ""
		}
	}
	if compiled.pattern != nil && !compiled.pattern.MatchString(rawName) {
		return false, ""
	}
	if len(matchedRequired) == 0 && compiled.pattern == nil {
		return false, ""
	}
	return true, fmt.Sprintf("required=%s", strings.Join(matchedRequired, ","))
}

func containsTerm(normalized, term string) bool {
	if term == "" {
		return false
	}
	needle := " " + term + " "
	return strings.Contains(" "+normalized+" ", needle)
}

func normalizeTerms(terms []string) []string {
	result := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		normalized := Normalize(term)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

// SeedRules returns a small set of example matching rules that demonstrate
// the different matching capabilities: pure required-terms, excluded terms,
// regex patterns, aliases, and priority-based specificity. These are seeded
// on first launch so new users can see how rules work. Administrators add
// their own rules through the console.
func SeedRules() []Rule {
	return []Rule{
		// Pure required-terms match — the simplest rule form.
		{Provider: "DeepSeek", CanonicalName: "deepseek-chat", RequiredTerms: []string{"deepseek", "chat"}, Priority: 100, Enabled: true},

		// Required + excluded terms: match the base model but not its variants.
		{Provider: "OpenAI", CanonicalName: "gpt-4o", RequiredTerms: []string{"gpt", "4o"}, ExcludedTerms: []string{"mini", "audio"}, Priority: 100, Enabled: true},

		// Higher-priority specific variant that would also match the base rule above.
		{Provider: "OpenAI", CanonicalName: "gpt-4o-mini", RequiredTerms: []string{"gpt", "4o", "mini"}, Priority: 110, Enabled: true},

		// Regex pattern for precise version-boundary matching.
		{Provider: "Anthropic", CanonicalName: "claude-sonnet-4", RequiredTerms: []string{"claude", "sonnet", "4"}, Pattern: `(?i)(^|[^a-z0-9])(?:claude[^a-z0-9]+)?sonnet[^0-9]+4([^0-9]|$)`, Priority: 100, Enabled: true},

		// Aliases: alternate names that also match this rule (acts as an
		// additional "any-of" requirement alongside the required terms).
		{Provider: "Google", CanonicalName: "gemini-pro", RequiredTerms: []string{"gemini"}, AnyTerms: []string{"pro"}, Aliases: []string{"ultra"}, ExcludedTerms: []string{"flash"}, Priority: 100, Enabled: true},
	}
}
