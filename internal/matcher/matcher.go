package matcher

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
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
	RawName   string
	Matches   []Match
	Ambiguous bool
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

// New compiles rules for matching. Terms are case-insensitive literal
// substrings of the raw model name: punctuation is significant, so "5-5" and
// "5.5" are different terms and upstream spellings must be listed separately.
// Rules that need boundary precision (e.g. "5" must not hit "5.5") should use
// Pattern, a Go regex applied to the raw name.
func New(rules []Rule) (*Engine, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.CanonicalName) == "" {
			return nil, fmt.Errorf("rule canonical name is required")
		}
		entry := compiledRule{
			rule:          rule,
			requiredTerms: compileTerms(rule.RequiredTerms),
			anyTerms:      compileTerms(append(append([]string{}, rule.AnyTerms...), rule.Aliases...)),
			excludedTerms: compileTerms(rule.ExcludedTerms),
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
	preview := Preview{RawName: rawName}
	for _, compiled := range engine.rules {
		if !compiled.rule.Enabled {
			continue
		}
		if matched, explanation := matchRule(compiled, rawName); matched {
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

func matchRule(compiled compiledRule, rawName string) (bool, string) {
	haystack := strings.ToLower(rawName)
	matchedRequired := make([]string, 0, len(compiled.requiredTerms))
	for _, term := range compiled.requiredTerms {
		if !strings.Contains(haystack, term) {
			return false, ""
		}
		matchedRequired = append(matchedRequired, term)
	}
	if len(compiled.anyTerms) > 0 {
		matchedAny := ""
		for _, term := range compiled.anyTerms {
			if strings.Contains(haystack, term) {
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
		if strings.Contains(haystack, term) {
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

// compileTerms lowercases and deduplicates match terms, keeping punctuation
// verbatim — terms are literal substrings of the raw model name.
func compileTerms(terms []string) []string {
	result := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		result = append(result, term)
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
