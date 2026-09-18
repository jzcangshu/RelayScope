package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AnnouncementInput is the adapter-facing DTO for a single announcement.
type AnnouncementInput struct {
	ExternalID  string
	Title       string
	Content     string
	AnnType     string
	Extra       string
	PublishedAt time.Time
}

// ApplyAnnouncements upserts announcements for a site and returns only the
// newly appeared entries (first time seen or content changed since last run).
func (store *Store) ApplyAnnouncements(ctx context.Context, siteID int64, anns []AnnouncementInput, now time.Time) ([]SiteAnnouncement, error) {
	if len(anns) == 0 {
		return nil, nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin apply announcements: %w", err)
	}
	defer tx.Rollback()

	nowMs := unixMilli(now)
	news := make([]SiteAnnouncement, 0, len(anns))
	for _, ann := range anns {
		externalID := strings.TrimSpace(ann.ExternalID)
		if externalID == "" {
			continue
		}
		hash := contentHash(ann.Content, ann.AnnType, ann.Extra)
		publishedMs := unixMilli(ann.PublishedAt)
		if publishedMs == 0 {
			publishedMs = nowMs
		}

		var existingHash string
		var existingID int64
		err := tx.QueryRowContext(ctx,
			`SELECT id, content_hash FROM site_announcements WHERE site_id = ? AND external_id = ?`,
			siteID, externalID).Scan(&existingID, &existingHash)

		if err == sql.ErrNoRows {
			res, insertErr := tx.ExecContext(ctx,
				`INSERT INTO site_announcements (site_id, external_id, title, content, ann_type, extra, content_hash, published_at, first_seen_at, last_seen_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				siteID, externalID, strings.TrimSpace(ann.Title), ann.Content,
				defaultAnnType(ann.AnnType), strings.TrimSpace(ann.Extra),
				hash, publishedMs, nowMs, nowMs)
			if insertErr != nil {
				return nil, fmt.Errorf("insert announcement %s: %w", externalID, insertErr)
			}
			id, _ := res.LastInsertId()
			news = append(news, SiteAnnouncement{
				ID: id, SiteID: siteID, ExternalID: externalID,
				Title: strings.TrimSpace(ann.Title), Content: ann.Content,
				AnnType: defaultAnnType(ann.AnnType), Extra: strings.TrimSpace(ann.Extra),
				ContentHash: hash,
				PublishedAt: time.UnixMilli(publishedMs).UTC(),
				FirstSeenAt: now, LastSeenAt: now,
			})
		} else if err != nil {
			return nil, fmt.Errorf("query announcement %s: %w", externalID, err)
		} else {
			_, updateErr := tx.ExecContext(ctx,
				`UPDATE site_announcements SET title = ?, content = ?, ann_type = ?, extra = ?, content_hash = ?, last_seen_at = ?, removed_at = NULL
				 WHERE id = ?`,
				strings.TrimSpace(ann.Title), ann.Content,
				defaultAnnType(ann.AnnType), strings.TrimSpace(ann.Extra),
				hash, nowMs, existingID)
			if updateErr != nil {
				return nil, fmt.Errorf("update announcement %s: %w", externalID, updateErr)
			}
			if existingHash != hash {
				news = append(news, SiteAnnouncement{
					ID: existingID, SiteID: siteID, ExternalID: externalID,
					Title: strings.TrimSpace(ann.Title), Content: ann.Content,
					AnnType: defaultAnnType(ann.AnnType), Extra: strings.TrimSpace(ann.Extra),
					ContentHash: hash,
					PublishedAt: time.UnixMilli(publishedMs).UTC(),
					LastSeenAt:  now,
				})
			}
		}
	}

	if _, err := incrementRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit apply announcements: %w", err)
	}
	return news, nil
}

// ListSiteAnnouncements returns recent announcements for a site.
func (store *Store) ListSiteAnnouncements(ctx context.Context, siteID int64, limit int) ([]SiteAnnouncement, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT id, site_id, external_id, title, content, ann_type, extra, published_at, first_seen_at, last_seen_at, removed_at
		 FROM site_announcements
		 WHERE site_id = ? AND removed_at IS NULL
		 ORDER BY published_at DESC
		 LIMIT ?`, siteID, limit)
	if err != nil {
		return nil, fmt.Errorf("list site announcements: %w", err)
	}
	defer rows.Close()
	return scanSiteAnnouncements(rows)
}

// ListAllRecentAnnouncements returns the most recent announcements across all sites.
func (store *Store) ListAllRecentAnnouncements(ctx context.Context, limit int) ([]SiteAnnouncement, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT a.id, a.site_id, s.name, a.external_id, a.title, a.content, a.ann_type, a.extra, a.published_at, a.first_seen_at, a.last_seen_at, a.removed_at
		 FROM site_announcements a
		 JOIN sites s ON s.id = a.site_id
		 WHERE a.removed_at IS NULL
		 ORDER BY a.published_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list all recent announcements: %w", err)
	}
	defer rows.Close()
	return scanAllAnnouncements(rows)
}

// SiteIDsWithAnnouncements returns site IDs that have at least one non-removed announcement.
func (store *Store) SiteIDsWithAnnouncements(ctx context.Context) ([]int64, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT DISTINCT site_id FROM site_announcements WHERE removed_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("site ids with announcements: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan site id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CleanupOldAnnouncements removes announcements older than maxAge that are already marked removed.
func (store *Store) CleanupOldAnnouncements(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := unixMilli(time.Now().UTC().Add(-maxAge))
	result, err := store.db.ExecContext(ctx,
		`DELETE FROM site_announcements WHERE last_seen_at < ? AND removed_at IS NOT NULL`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup old announcements: %w", err)
	}
	return result.RowsAffected()
}

// scanSiteAnnouncements scans rows from the single-site query (no site_name column).
func scanSiteAnnouncements(rows *sql.Rows) ([]SiteAnnouncement, error) {
	var items []SiteAnnouncement
	for rows.Next() {
		var a SiteAnnouncement
		var removedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.SiteID, &a.ExternalID, &a.Title, &a.Content, &a.AnnType, &a.Extra, &a.PublishedAt, &a.FirstSeenAt, &a.LastSeenAt, &removedAt); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		if removedAt.Valid {
			t := time.UnixMilli(removedAt.Int64).UTC()
			a.RemovedAt = &t
		}
		a.PublishedAt = time.UnixMilli(a.PublishedAt.UnixMilli()).UTC()
		a.FirstSeenAt = time.UnixMilli(a.FirstSeenAt.UnixMilli()).UTC()
		a.LastSeenAt = time.UnixMilli(a.LastSeenAt.UnixMilli()).UTC()
		items = append(items, a)
	}
	return items, rows.Err()
}

// scanAllAnnouncements scans rows from the all-sites query (includes site_name column).
func scanAllAnnouncements(rows *sql.Rows) ([]SiteAnnouncement, error) {
	var items []SiteAnnouncement
	for rows.Next() {
		var a SiteAnnouncement
		var removedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.SiteID, &a.SiteName, &a.ExternalID, &a.Title, &a.Content, &a.AnnType, &a.Extra, &a.PublishedAt, &a.FirstSeenAt, &a.LastSeenAt, &removedAt); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		if removedAt.Valid {
			t := time.UnixMilli(removedAt.Int64).UTC()
			a.RemovedAt = &t
		}
		a.PublishedAt = time.UnixMilli(a.PublishedAt.UnixMilli()).UTC()
		a.FirstSeenAt = time.UnixMilli(a.FirstSeenAt.UnixMilli()).UTC()
		a.LastSeenAt = time.UnixMilli(a.LastSeenAt.UnixMilli()).UTC()
		items = append(items, a)
	}
	return items, rows.Err()
}

func contentHash(content, annType, extra string) string {
	h := sha256.Sum256([]byte(content + "|" + annType + "|" + extra))
	return hex.EncodeToString(h[:])
}

func defaultAnnType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "default"
	}
	return t
}
