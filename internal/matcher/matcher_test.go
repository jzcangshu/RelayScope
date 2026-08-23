package matcher

import "testing"

func TestNormalizeAndBoundaryMatching(t *testing.T) {
	t.Parallel()

	if got := Normalize(" OpenAI/gpt_5.6-SOL (thinking) "); got != "openai gpt 5 6 sol thinking" {
		t.Fatalf("Normalize() = %q", got)
	}
	engine, err := New([]Rule{{
		CanonicalName: "gpt-5.6-sol",
		RequiredTerms: []string{"5.6", "sol"},
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if preview := engine.Preview("provider/gpt-5.6-sol-thinking"); len(preview.Matches) != 1 {
		t.Fatalf("expected suffix variant to match: %+v", preview)
	}
	if preview := engine.Preview("provider/gpt-5.6-solid"); len(preview.Matches) != 0 {
		t.Fatalf("solid must not match sol: %+v", preview)
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
