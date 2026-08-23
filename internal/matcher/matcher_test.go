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

func TestGeneratedClaudeRulesAndAmbiguity(t *testing.T) {
	t.Parallel()

	rules := SeedRules()
	engine, err := New(rules)
	if err != nil {
		t.Fatalf("new seed engine: %v", err)
	}
	preview := engine.Preview("anthropic/claude-sonnet-4-7-thinking")
	if len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "claude-sonnet-4-7" {
		t.Fatalf("unexpected Claude match: %+v", preview)
	}
	if preview := engine.Preview("opus-4.8"); len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "claude-opus-4-8" {
		t.Fatalf("provider-prefix-free Claude alias did not match: %+v", preview)
	}
	ambiguousEngine, err := New([]Rule{
		{CanonicalName: "first", RequiredTerms: []string{"model"}, Priority: 1, Enabled: true},
		{CanonicalName: "second", RequiredTerms: []string{"model"}, Priority: 1, Enabled: true},
	})
	if err != nil {
		t.Fatalf("new ambiguous engine: %v", err)
	}
	if preview := ambiguousEngine.Preview("model"); !preview.Ambiguous {
		t.Fatalf("expected ambiguity: %+v", preview)
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

func TestSeedRulesAvoidVersionSubstringFalseMatches(t *testing.T) {
	engine, err := New(SeedRules())
	if err != nil {
		t.Fatal(err)
	}
	if preview := engine.Preview("glm-4.5"); len(preview.Matches) != 0 {
		t.Fatalf("glm-4.5 should not match glm-5: %+v", preview.Matches)
	}
	preview := engine.Preview("claude-haiku-4-5")
	for _, match := range preview.Matches {
		if match.Rule.CanonicalName == "claude-haiku-5" || match.Rule.CanonicalName == "claude-haiku-4-6" || match.Rule.CanonicalName == "claude-haiku-4-7" || match.Rule.CanonicalName == "claude-haiku-4-8" {
			t.Fatalf("unexpected Claude version match: %+v", preview.Matches)
		}
	}
	for rawName, canonical := range map[string]string{
		"deepseek-v4-flash-0731-free": "deepseek-v4-flash-0731",
		"deepseek-v4-pro-0731":        "deepseek-v4-pro-0731",
		"mimo-v2.5-pro-thinking":      "mimo-v2.5-pro",
	} {
		preview := engine.Preview(rawName)
		if preview.Ambiguous || len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != canonical || !preview.Matches[0].Primary {
			t.Fatalf("%s should uniquely match %s: %+v", rawName, canonical, preview)
		}
	}
}

func TestSeedRulesKeepGPT5NanoDistinct(t *testing.T) {
	engine, err := New(SeedRules())
	if err != nil {
		t.Fatal(err)
	}
	for _, rawName := range []string{"gpt-5-nano", "openai/gpt_5_nano-thinking", "GPT 5 nano"} {
		preview := engine.Preview(rawName)
		if preview.Ambiguous || len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "gpt-5-nano" || !preview.Matches[0].Primary {
			t.Fatalf("%q should uniquely match gpt-5-nano: %+v", rawName, preview)
		}
	}
	for _, rawName := range []string{"gpt-5.5", "gpt-5.6-sol", "gpt-5-nanobot"} {
		preview := engine.Preview(rawName)
		for _, match := range preview.Matches {
			if match.Rule.CanonicalName == "gpt-5-nano" {
				t.Fatalf("%q must not match gpt-5-nano: %+v", rawName, preview)
			}
		}
	}
}

func TestSeedRulesCoverHXIStatusPageModels(t *testing.T) {
	engine, err := New(SeedRules())
	if err != nil {
		t.Fatal(err)
	}
	for rawName, canonical := range map[string]string{
		"gpt-5.4":                    "gpt-5.4",
		"gpt-5.5":                    "gpt-5.5",
		"gpt-5.6-luna":               "gpt-5.6-luna",
		"gpt-5.6-sol":                "gpt-5.6-sol",
		"gpt-5.6-terra":              "gpt-5.6-terra",
		"deepseek/deepseek-v4-flash": "deepseek-v4-flash",
		"deepseek/deepseek-v4-pro":   "deepseek-v4-pro",
		"z-ai/glm-5.2":               "glm-5.2",
		"z-ai/glm-5.3":               "glm-5.3",
		"minimax/minimax-m3":         "minimax-m3",
		"moonshotai/kimi-k2.6":       "kimi-k2.6",
	} {
		preview := engine.Preview(rawName)
		if preview.Ambiguous || len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != canonical || !preview.Matches[0].Primary {
			t.Fatalf("%q should uniquely match %q: %+v", rawName, canonical, preview)
		}
	}
}

func TestSeedRulesCoverRequestedGeminiModels(t *testing.T) {
	engine, err := New(SeedRules())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"gemini-3.7-flash":                   "gemini-3.7-flash",
		"gemini-3.6-flash-thinking":          "gemini-3.6-flash",
		"google/gemini-3.5-flash":            "gemini-3.5-flash",
		"gemini-3.5-flash-lite":              "gemini-3.5-flash-lite",
		"gemini-3.1-flash-lite":              "gemini-3.1-flash-lite",
		"gemini-3.1-pro-preview":             "gemini-3.1-pro-preview",
		"gemini-3.1-pro-preview-customtools": "gemini-3.1-pro-preview-customtools",
		"gemini-3-flash-preview":             "gemini-3-flash-preview",
		"gemini-2.5-pro":                     "gemini-2.5-pro",
		"gemini-2.5-flash":                   "gemini-2.5-flash",
		"gemini-2.5-flash-lite":              "gemini-2.5-flash-lite",
	}
	for rawName, canonical := range want {
		preview := engine.Preview(rawName)
		if preview.Ambiguous || len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != canonical || !preview.Matches[0].Primary {
			t.Errorf("%q should uniquely match %q: %+v", rawName, canonical, preview)
		}
	}
	for _, rawName := range []string{"gemini-3.5-flash-lite", "gemini-3.1-pro-preview-customtools", "gemini-2.5-flash-lite"} {
		preview := engine.Preview(rawName)
		for _, match := range preview.Matches {
			if match.Rule.CanonicalName == "gemini-3.5-flash" || match.Rule.CanonicalName == "gemini-3.1-pro-preview" || match.Rule.CanonicalName == "gemini-2.5-flash" {
				t.Errorf("%q matched base Gemini rule %q: %+v", rawName, match.Rule.CanonicalName, preview)
			}
		}
	}
}

func TestSeedRulesCoverGrok46(t *testing.T) {
	engine, err := New(SeedRules())
	if err != nil {
		t.Fatal(err)
	}
	preview := engine.Preview("grok-4.6")
	if preview.Ambiguous || len(preview.Matches) != 1 || preview.Matches[0].Rule.CanonicalName != "grok-4.6" || !preview.Matches[0].Primary {
		t.Fatalf("grok-4.6 should uniquely match its rule: %+v", preview)
	}
}
