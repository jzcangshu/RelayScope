package bootstrap

import (
	"context"

	"relayscope/internal/matcher"
	"relayscope/internal/store"
)

func EnsureInitialRules(ctx context.Context, dbStore *store.Store) error {
	existing, err := dbStore.ListRuleNames(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		seen[name] = struct{}{}
	}
	for _, rule := range matcher.SeedRules() {
		if _, exists := seen[rule.CanonicalName]; exists {
			continue
		}
		if err := dbStore.CreateRule(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}
