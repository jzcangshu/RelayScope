package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"relayscope/internal/routing"
)

const routingInstanceSetting = "routing_source_instance_id"

var ErrRoutingScope = errors.New("invalid routing observation site scope")

func (store *Store) ensureRoutingIdentity(ctx context.Context) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate routing identity: %w", err)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO app_meta(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING`, routingInstanceSetting, "rs_"+hex.EncodeToString(random[:]))
	return err
}

// QueryRoutingObservations reads identity, revision, site scope, and rows from
// one SQLite snapshot. It neither reuses the dashboard's minimum-price fallback
// nor joins canonical matches that could hide configured raw model names.
func (store *Store) QueryRoutingObservations(ctx context.Context, siteIDs []int64, now time.Time) (routing.Envelope, error) {
	if len(siteIDs) > routing.MaxSites {
		return routing.Envelope{}, ErrRoutingScope
	}
	seenSites := make(map[int64]bool, len(siteIDs))
	requested := make([]string, 0, len(siteIDs))
	args := make([]any, 0, len(siteIDs))
	for _, id := range siteIDs {
		if id <= 0 || seenSites[id] {
			return routing.Envelope{}, ErrRoutingScope
		}
		seenSites[id] = true
		requested = append(requested, strconv.FormatInt(id, 10))
		args = append(args, id)
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routing.Envelope{}, err
	}
	defer tx.Rollback()
	envelope := routing.Envelope{SchemaVersion: 1, GeneratedAt: now.UnixMilli(), RequestedSiteIDs: requested, Observations: make([]routing.Observation, 0)}
	err = tx.QueryRowContext(ctx, `SELECT (SELECT value FROM app_meta WHERE key=?),(SELECT value FROM app_meta WHERE key='data_revision')`, routingInstanceSetting).Scan(&envelope.SourceInstanceID, &envelope.Revision)
	if err != nil {
		return routing.Envelope{}, err
	}
	if strings.TrimSpace(envelope.SourceInstanceID) == "" {
		return routing.Envelope{}, errors.New("routing identity is missing")
	}
	if _, err := strconv.ParseUint(envelope.Revision, 10, 64); err != nil {
		return routing.Envelope{}, errors.New("routing revision is invalid")
	}
	if len(siteIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(siteIDs)), ",")
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE id IN (`+placeholders+`) AND enabled=1 AND deleted_at IS NULL`, args...).Scan(&count); err != nil {
			return routing.Envelope{}, err
		}
		if count != len(siteIDs) {
			return routing.Envelope{}, ErrRoutingScope
		}
		rows, err := tx.QueryContext(ctx, `SELECT site.id, raw.raw_name, groups.raw_name, groups.source_extension,
			site.interval_seconds, site.acquisition_state
			FROM sites site JOIN raw_models raw ON raw.site_id=site.id
			JOIN site_groups groups ON groups.raw_model_id=raw.id
			JOIN current_snapshots snapshot ON snapshot.group_id=groups.id
			WHERE site.id IN (`+placeholders+`) AND raw.removed_at IS NULL
			ORDER BY site.id,raw.raw_name,groups.raw_name LIMIT ?`, append(args, routing.MaxRows+1)...)
		if err != nil {
			return routing.Envelope{}, err
		}
		observations, err := readRoutingRows(rows)
		rows.Close()
		if err != nil {
			return routing.Envelope{}, err
		}
		envelope.Observations = observations
	}
	if err := tx.Commit(); err != nil {
		return routing.Envelope{}, err
	}
	return envelope, nil
}

func readRoutingRows(rows *sql.Rows) ([]routing.Observation, error) {
	result := make([]routing.Observation, 0)
	identities := make(map[string]bool)
	count := 0
	for rows.Next() {
		count++
		if count > routing.MaxRows {
			return nil, errors.New("routing response exceeds row limit")
		}
		var siteID, interval int64
		var model, group, acquisition string
		var extension sql.NullString
		if err := rows.Scan(&siteID, &model, &group, &extension, &interval, &acquisition); err != nil {
			return nil, err
		}
		if strings.TrimSpace(model) == "" {
			return nil, errors.New("routing observation has invalid model identity")
		}
		metadata := routing.ReadExtension(nullableBytes(extension))
		row := routing.Observation{SiteID: strconv.FormatInt(siteID, 10), RawModelName: model, Availability: routing.UnknownAvailability()}
		if metadata.GroupKnown != nil && *metadata.GroupKnown && strings.TrimSpace(group) != "" {
			row.RawGroupName = &group
		}
		if metadata.Availability != nil {
			row.Availability = *metadata.Availability
		}
		row.Price = metadata.Price
		if row.RawGroupName == nil {
			row.Price = nil
			row.Availability = routing.UnknownAvailability()
		}
		if interval > 0 && interval <= 9_007_199_254_740_991/1000 {
			milliseconds := interval * 1000
			row.Availability.SuggestedIntervalMilliseconds = &milliseconds
			if row.Price != nil && row.Price.SuggestedIntervalMilliseconds == nil {
				row.Price.SuggestedIntervalMilliseconds = &milliseconds
			}
		}
		if acquisition != "fresh" && row.Availability.Evidence != "unknown" {
			row.Availability.CollectionStatus = "error"
		}
		keyParts, _ := json.Marshal([]any{row.SiteID, row.RawModelName, row.RawGroupName})
		key := string(keyParts)
		if identities[key] {
			// Multiple old placeholder groups collapse into the same unknown
			// identity. None has trusted price or health evidence to retain.
			if row.RawGroupName == nil {
				continue
			}
			return nil, errors.New("routing observation identity conflict")
		}
		identities[key] = true
		result = append(result, row)
	}
	return result, rows.Err()
}
