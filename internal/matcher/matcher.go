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
	rule    Rule
	pattern *regexp.Regexp
}

func New(rules []Rule) (*Engine, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.CanonicalName) == "" {
			return nil, fmt.Errorf("rule canonical name is required")
		}
		if rule.Pattern == "" {
			compiled = append(compiled, compiledRule{rule: rule})
			continue
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile rule %q pattern: %w", rule.CanonicalName, err)
		}
		compiled = append(compiled, compiledRule{rule: rule, pattern: pattern})
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
	rule := compiled.rule
	required := normalizeTerms(rule.RequiredTerms)
	any := normalizeTerms(append(append([]string{}, rule.AnyTerms...), rule.Aliases...))
	excluded := normalizeTerms(rule.ExcludedTerms)

	matchedRequired := make([]string, 0, len(required))
	for _, term := range required {
		if !containsTerm(normalized, term) {
			return false, ""
		}
		matchedRequired = append(matchedRequired, term)
	}
	if len(any) > 0 {
		matchedAny := ""
		for _, term := range any {
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
	for _, term := range excluded {
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

func SeedRules() []Rule {
	rules := []Rule{
		{Provider: "DeepSeek", CanonicalName: "deepseek-v4-flash", RequiredTerms: []string{"deepseek", "v4", "flash"}, ExcludedTerms: []string{"0731"}, Priority: 100, Enabled: true},
		{Provider: "DeepSeek", CanonicalName: "deepseek-v4-pro", RequiredTerms: []string{"deepseek", "v4", "pro"}, ExcludedTerms: []string{"0731"}, Priority: 100, Enabled: true},
		{Provider: "DeepSeek", CanonicalName: "deepseek-v4-flash-0731", RequiredTerms: []string{"deepseek", "v4", "flash", "0731"}, Priority: 110, Enabled: true},
		{Provider: "DeepSeek", CanonicalName: "deepseek-v4-pro-0731", RequiredTerms: []string{"deepseek", "v4", "pro", "0731"}, Priority: 110, Enabled: true},
		{Provider: "GLM", CanonicalName: "glm-5", RequiredTerms: []string{"glm", "5"}, Pattern: `(?i)(^|[^0-9])glm[^0-9]+5([^0-9.]|$)`, Priority: 90, Enabled: true},
		{Provider: "GLM", CanonicalName: "glm-5.1", RequiredTerms: []string{"glm", "5", "1"}, Pattern: `(?i)glm[^0-9]+5[._-]1([^0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "GLM", CanonicalName: "glm-5.2", RequiredTerms: []string{"glm", "5", "2"}, Pattern: `(?i)glm[^0-9]+5[._-]2([^0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "GLM", CanonicalName: "glm-5.3", RequiredTerms: []string{"glm", "5", "3"}, Pattern: `(?i)glm[^0-9]+5[._-]3([^0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "MiniMax", CanonicalName: "minimax-m2.5", RequiredTerms: []string{"minimax", "m2", "5"}, Priority: 100, Enabled: true},
		{Provider: "MiniMax", CanonicalName: "minimax-m2.7", RequiredTerms: []string{"minimax", "m2", "7"}, Priority: 100, Enabled: true},
		{Provider: "MiniMax", CanonicalName: "minimax-m3", RequiredTerms: []string{"minimax", "m3"}, Priority: 100, Enabled: true},
		{Provider: "Kimi", CanonicalName: "kimi-k2.5", RequiredTerms: []string{"kimi", "k2", "5"}, Priority: 100, Enabled: true},
		{Provider: "Kimi", CanonicalName: "kimi-k2.6", RequiredTerms: []string{"kimi", "k2", "6"}, Priority: 100, Enabled: true},
		{Provider: "Kimi", CanonicalName: "kimi-k2.7", RequiredTerms: []string{"kimi", "k2", "7"}, Priority: 100, Enabled: true},
		{Provider: "Kimi", CanonicalName: "kimi-k3", RequiredTerms: []string{"kimi", "k3"}, Priority: 100, Enabled: true},
		{Provider: "MiMo", CanonicalName: "mimo-v2.5", RequiredTerms: []string{"mimo", "v2", "5"}, ExcludedTerms: []string{"pro"}, Priority: 100, Enabled: true},
		{Provider: "MiMo", CanonicalName: "mimo-v2.5-pro", RequiredTerms: []string{"mimo", "v2", "5", "pro"}, Priority: 110, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5.4", RequiredTerms: []string{"gpt", "5", "4"}, Pattern: `(?i)gpt[ _./-]*5[._-]*4([^0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5.5", RequiredTerms: []string{"gpt", "5", "5"}, Pattern: `(?i)gpt[ _./-]*5[._-]*5([^0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5-nano", RequiredTerms: []string{"gpt", "5", "nano"}, Pattern: `(?i)(^|[^a-z0-9])gpt[ _./-]*5[ _.-]*nano([^a-z0-9]|$)`, Priority: 120, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5.6-luna", RequiredTerms: []string{"gpt", "5", "6", "luna"}, Priority: 110, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5.6-terra", RequiredTerms: []string{"gpt", "5", "6", "terra"}, Priority: 110, Enabled: true},
		{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Priority: 110, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.7-flash", RequiredTerms: []string{"gemini", "3.7", "flash"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*7[ _./-]*flash([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.6-flash", RequiredTerms: []string{"gemini", "3.6", "flash"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*6[ _./-]*flash([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.5-flash", RequiredTerms: []string{"gemini", "3.5", "flash"}, ExcludedTerms: []string{"lite"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*5[ _./-]*flash([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.5-flash-lite", RequiredTerms: []string{"gemini", "3.5", "flash", "lite"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*5[ _./-]*flash[ _./-]*lite([^a-z0-9]|$)`, Priority: 110, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.1-flash-lite", RequiredTerms: []string{"gemini", "3.1", "flash", "lite"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*1[ _./-]*flash[ _./-]*lite([^a-z0-9]|$)`, Priority: 110, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.1-pro-preview", RequiredTerms: []string{"gemini", "3.1", "pro", "preview"}, ExcludedTerms: []string{"customtools"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*1[ _./-]*pro[ _./-]*preview([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3.1-pro-preview-customtools", RequiredTerms: []string{"gemini", "3.1", "pro", "preview", "customtools"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[._-]*1[ _./-]*pro[ _./-]*preview[ _./-]*customtools([^a-z0-9]|$)`, Priority: 120, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-3-flash-preview", RequiredTerms: []string{"gemini", "3", "flash", "preview"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*3[ _./-]*flash[ _./-]*preview([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-2.5-pro", RequiredTerms: []string{"gemini", "2.5", "pro"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*2[._-]*5[ _./-]*pro([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-2.5-flash", RequiredTerms: []string{"gemini", "2.5", "flash"}, ExcludedTerms: []string{"lite"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*2[._-]*5[ _./-]*flash([^a-z0-9]|$)`, Priority: 100, Enabled: true},
		{Provider: "Google", CanonicalName: "gemini-2.5-flash-lite", RequiredTerms: []string{"gemini", "2.5", "flash", "lite"}, Pattern: `(?i)(^|[^a-z0-9])gemini[ _./-]*2[._-]*5[ _./-]*flash[ _./-]*lite([^a-z0-9]|$)`, Priority: 110, Enabled: true},
		{Provider: "xAI", CanonicalName: "grok-4.3", RequiredTerms: []string{"grok", "4", "3"}, Priority: 100, Enabled: true},
		{Provider: "xAI", CanonicalName: "grok-4.5", RequiredTerms: []string{"grok", "4", "5"}, Priority: 100, Enabled: true},
		{Provider: "xAI", CanonicalName: "grok-4.6", RequiredTerms: []string{"grok", "4", "6"}, Priority: 100, Enabled: true},
		{Provider: "Anthropic", CanonicalName: "claude-fable-5", RequiredTerms: []string{"claude", "fable", "5"}, Priority: 120, Enabled: true, Generated: true},
	}
	for _, family := range []string{"opus", "sonnet", "haiku"} {
		for _, version := range []string{"4-6", "4-7", "4-8", "5"} {
			canonical := "claude-" + family + "-" + version
			patternVersion := strings.ReplaceAll(version, "-", `[._-]`)
			rules = append(rules, Rule{Provider: "Anthropic", CanonicalName: canonical, RequiredTerms: []string{family, version}, Pattern: `(?i)(?:^|[^a-z0-9])(?:claude[^a-z0-9]+)?` + family + `[^0-9]+` + patternVersion + `([^0-9]|$)`, Priority: 100, Enabled: true, Generated: true})
		}
	}
	return rules
}
