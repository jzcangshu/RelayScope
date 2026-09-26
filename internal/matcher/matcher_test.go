package matcher

import "testing"

func TestSubstringMatchingDistinguishesVersions(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{{
		Provider:      "Anthropic",
		CanonicalName: "claude-opus-5-5",
		RequiredTerms: []string{"Opus"},
		AnyTerms:      []string{"5-5", "5.5"},
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if preview := engine.Preview("claude-opus-5-5"); len(preview.Matches) != 1 {
		t.Fatalf("claude-opus-5-5 should match: %+v", preview)
	}
	if preview := engine.Preview("Claude-Opus-5.5-thinking"); len(preview.Matches) != 1 {
		t.Fatalf("variant spellings listed in anyTerms should match, case-insensitively: %+v", preview)
	}
	// The base version lacks the "5-5"/"5.5" substring and must not match.
	if preview := engine.Preview("claude-opus-5"); len(preview.Matches) != 0 {
		t.Fatalf("claude-opus-5 must not match the 5-5 rule: %+v", preview)
	}
}

func TestSubstringMatchingIsLiteralAboutPunctuation(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{{
		CanonicalName: "glm-5",
		RequiredTerms: []string{"glm"},
		AnyTerms:      []string{"5-5"},
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	// Terms are literal substrings: "5-5" does not appear in "glm-55".
	if preview := engine.Preview("glm-55"); len(preview.Matches) != 0 {
		t.Fatalf("glm-55 must not match a 5-5 term: %+v", preview)
	}
}

func TestExcludedTermsCarveOutSubstringOverlaps(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{{
		CanonicalName: "gpt-5.6-sol",
		RequiredTerms: []string{"5.6", "sol"},
		ExcludedTerms: []string{"solid"},
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if preview := engine.Preview("provider/gpt-5.6-sol-thinking"); len(preview.Matches) != 1 {
		t.Fatalf("suffix variant should match: %+v", preview)
	}
	// Substring matching hits "sol" inside "solid"; the excluded term carves it out.
	if preview := engine.Preview("provider/gpt-5.6-solid"); len(preview.Matches) != 0 {
		t.Fatalf("solid must be carved out by the excluded term: %+v", preview)
	}
}

func TestMultipleMatchesBecomeConflictWithoutPrimary(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{
		{CanonicalName: "gpt-5.6", RequiredTerms: []string{"gpt", "5", "6"}, Priority: 10, Enabled: true},
		{CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Priority: 20, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	preview := engine.Preview("gpt-5.6-sol")
	if len(preview.Matches) != 2 || !preview.Ambiguous || preview.Matches[0].Primary || preview.Matches[1].Primary {
		t.Fatalf("multiple matches must have no primary: %+v", preview)
	}
}

func TestSeedRulesAreValidAndNonEmpty(t *testing.T) {
	rules := SeedRules()
	if len(rules) == 0 {
		t.Fatal("SeedRules should return example rules for new users")
	}
	engine, err := New(rules)
	if err != nil {
		t.Fatalf("seed rules must produce a valid engine: %v", err)
	}
	if len(engine.rules) != len(rules) {
		t.Fatalf("engine rule count = %d, want %d", len(engine.rules), len(rules))
	}
}

func TestExcludedTermsPreventVariantMatches(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{
		{CanonicalName: "gpt-4o", RequiredTerms: []string{"gpt", "4o"}, ExcludedTerms: []string{"mini"}, Priority: 100, Enabled: true},
		{CanonicalName: "gpt-4o-mini", RequiredTerms: []string{"gpt", "4o", "mini"}, Priority: 110, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	// Base model matches.
	if preview := engine.Preview("gpt-4o"); len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "gpt-4o" {
		t.Fatalf("gpt-4o should match base rule: %+v", preview)
	}
	// Variant does NOT match the base (excluded), matches the specific rule instead.
	if preview := engine.Preview("gpt-4o-mini"); len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "gpt-4o-mini" || !preview.Matches[0].Primary {
		t.Fatalf("gpt-4o-mini should uniquely match its rule: %+v", preview)
	}
}

func TestPatternPreventsVersionSubstringFalseMatches(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{
		{CanonicalName: "claude-sonnet-4", RequiredTerms: []string{"claude", "sonnet", "4"}, Pattern: `(?i)(^|[^a-z0-9])(?:claude[^a-z0-9]+)?sonnet[^0-9]+4([^0-9]|$)`, Priority: 100, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if preview := engine.Preview("claude-sonnet-4"); len(preview.Matches) != 1 {
		t.Fatalf("claude-sonnet-4 should match: %+v", preview)
	}
	// Must NOT match version 45 — the pattern enforces a boundary after the 4.
	if preview := engine.Preview("claude-sonnet-45"); len(preview.Matches) != 0 {
		t.Fatalf("claude-sonnet-45 must not match claude-sonnet-4: %+v", preview.Matches)
	}
}

func TestAliasesProvideAlternateNames(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{
		{CanonicalName: "gemini-pro", RequiredTerms: []string{"gemini"}, AnyTerms: []string{"pro"}, Aliases: []string{"ultra"}, ExcludedTerms: []string{"flash"}, Priority: 100, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if preview := engine.Preview("gemini-pro"); len(preview.Matches) != 1 {
		t.Fatalf("gemini-pro should match: %+v", preview)
	}
	if preview := engine.Preview("gemini-ultra"); len(preview.Matches) != 1 {
		t.Fatalf("alias gemini-ultra should match: %+v", preview)
	}
	if preview := engine.Preview("gemini-flash"); len(preview.Matches) != 0 {
		t.Fatalf("gemini-flash must not match pro rule (excluded): %+v", preview.Matches)
	}
}

func TestAnyAndExcludeTerms(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{{
		CanonicalName: "reasoning-model",
		RequiredTerms: []string{"model"},
		AnyTerms:      []string{"thinking", "reasoning"},
		ExcludedTerms: []string{"lite"},
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if len(engine.Preview("model-thinking").Matches) != 1 || len(engine.Preview("model-lite-thinking").Matches) != 0 {
		t.Fatal("any/exclude terms behaved incorrectly")
	}
}

func TestAmbiguousMatchesHaveNoPrimary(t *testing.T) {
	t.Parallel()

	engine, err := New([]Rule{
		{CanonicalName: "first", RequiredTerms: []string{"model"}, Priority: 1, Enabled: true},
		{CanonicalName: "second", RequiredTerms: []string{"model"}, Priority: 1, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	preview := engine.Preview("model")
	if !preview.Ambiguous {
		t.Fatalf("expected ambiguity: %+v", preview)
	}
	for _, match := range preview.Matches {
		if match.Primary {
			t.Fatalf("ambiguous matches must have no primary: %+v", preview)
		}
	}
}
