package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	WishStatusOpen      = "open"
	WishStatusReached   = "reached"
	WishStatusRejected  = "rejected"
	WishStatusConnected = "connected"
)

var ErrWishDuplicate = errors.New("该站点已在许愿池中")

type WishSite struct {
	ID             int64      `json:"id"`
	Domain         string     `json:"domain"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	InviteRequired bool       `json:"inviteRequired"`
	TargetLDC      *int64     `json:"targetLdc"`
	Status         string     `json:"status"`
	CreatedBy      int64      `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	ResolvedAt     *time.Time `json:"resolvedAt"`
}

type WishSummary struct {
	WishSite
	PledgedLDC   int64             `json:"pledgedLdc"`
	Pledgers     int64             `json:"pledgers"`
	MyPledgedLDC int64             `json:"myPledgedLdc"`
	MyPending    bool              `json:"myPending"`
}

// NormalizeWishDomain 把用户输入的网址归一化为去重键：小写主机名、去掉前导 www.。
// 返回 (去重域名, 规范化访问地址, 错误)。
func NormalizeWishDomain(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("请填写访问网址")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", "", errors.New("网址格式不正确")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", errors.New("网址必须是 http(s) 链接")
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" || len(host) > 253 {
		return "", "", errors.New("网址格式不正确")
	}
	return host, "https://" + host, nil
}

func (s *Store) CreateWishSite(ctx context.Context, userID int64, name, rawURL string, inviteRequired bool, defaultTargetLDC int64) (WishSite, error) {
	if userID <= 0 {
		return WishSite{}, errors.New("请先登录")
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 60 {
		return WishSite{}, errors.New("请填写 60 字以内的站点名称")
	}
	domain, normalizedURL, err := NormalizeWishDomain(rawURL)
	if err != nil {
		return WishSite{}, err
	}
	var existing int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM wish_sites WHERE domain = ?`, domain).Scan(&existing); err == nil {
		return WishSite{}, ErrWishDuplicate
	} else if !errors.Is(err, sql.ErrNoRows) {
		return WishSite{}, err
	}
	var target any
	if !inviteRequired {
		target = defaultTargetLDC
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO wish_sites(domain, name, url, invite_required, target_ldc, status, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		domain, name, normalizedURL, boolToInt(inviteRequired), target, WishStatusOpen, userID, unixMilli(now))
	if err != nil {
		return WishSite{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return WishSite{}, err
	}
	wish := WishSite{ID: id, Domain: domain, Name: name, URL: normalizedURL, InviteRequired: inviteRequired, Status: WishStatusOpen, CreatedBy: userID, CreatedAt: now}
	if !inviteRequired {
		value := defaultTargetLDC
		wish.TargetLDC = &value
	}
	return wish, nil
}

// ListWishSites 返回 open + reached 的许愿站点（公开池），附带已支付进度；viewerID > 0 时附加本人认领额。
func (s *Store) ListWishSites(ctx context.Context, viewerID int64) ([]WishSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.id, w.domain, w.name, w.url, w.invite_required, w.target_ldc, w.status, w.created_by, w.created_at,
		COALESCE(SUM(CASE WHEN o.status = 'paid' THEN o.amount_ldc END), 0) AS pledged,
		COALESCE(COUNT(DISTINCT CASE WHEN o.status = 'paid' THEN o.user_id END), 0) AS pledgers
		FROM wish_sites w LEFT JOIN ldc_orders o ON o.wish_site_id = w.id
		WHERE w.status IN (?, ?)
		GROUP BY w.id
		ORDER BY CASE WHEN w.status = 'open' THEN 0 ELSE 1 END, pledged DESC, w.id ASC`, WishStatusOpen, WishStatusReached)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WishSummary{}
	for rows.Next() {
		var item WishSummary
		var invite, created int64
		if err := rows.Scan(&item.ID, &item.Domain, &item.Name, &item.URL, &invite, &item.TargetLDC, &item.Status, &item.CreatedBy, &created, &item.PledgedLDC, &item.Pledgers); err != nil {
			return nil, err
		}
		item.InviteRequired = invite == 1
		item.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if viewerID > 0 && len(items) > 0 {
		ids := make([]any, 0, len(items))
		placeholders := strings.TrimRight(strings.Repeat("?,", len(items)), ",")
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		query := fmt.Sprintf(`SELECT wish_site_id, COALESCE(SUM(amount_ldc), 0) FROM ldc_orders WHERE user_id = ? AND status = 'paid' AND wish_site_id IN (%s) GROUP BY wish_site_id`, placeholders)
		myRows, err := s.db.QueryContext(ctx, query, append([]any{viewerID}, ids...)...)
		if err != nil {
			return nil, err
		}
		defer myRows.Close()
		mine := map[int64]int64{}
		for myRows.Next() {
			var siteID, amount int64
			if err := myRows.Scan(&siteID, &amount); err != nil {
				return nil, err
			}
			mine[siteID] = amount
		}
		if err := myRows.Err(); err != nil {
			return nil, err
		}
		for i := range items {
			items[i].MyPledgedLDC = mine[items[i].ID]
		}
	}
	return items, nil
}

func (s *Store) GetWishSite(ctx context.Context, id int64) (WishSite, error) {
	var w WishSite
	var invite, created int64
	var resolved *int64
	err := s.db.QueryRowContext(ctx, `SELECT id, domain, name, url, invite_required, target_ldc, status, created_by, created_at, resolved_at FROM wish_sites WHERE id = ?`, id).
		Scan(&w.ID, &w.Domain, &w.Name, &w.URL, &invite, &w.TargetLDC, &w.Status, &w.CreatedBy, &created, &resolved)
	if err != nil {
		return WishSite{}, err
	}
	w.InviteRequired = invite == 1
	w.CreatedAt = time.UnixMilli(created).UTC()
	if resolved != nil {
		at := time.UnixMilli(*resolved).UTC()
		w.ResolvedAt = &at
	}
	return w, nil
}

// UpdateWishSite 管理端更新：目标额度（nil 表示不改）与状态（空串表示不改）。
func (s *Store) UpdateWishSite(ctx context.Context, id int64, targetLDC *int64, status string) (WishSite, error) {
	if targetLDC != nil {
		if *targetLDC <= 0 || *targetLDC > 1_000_000 {
			return WishSite{}, errors.New("目标额度需在 1-1000000 LDC 之间")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE wish_sites SET target_ldc = ? WHERE id = ?`, *targetLDC, id); err != nil {
			return WishSite{}, err
		}
	}
	switch status {
	case "":
	case WishStatusOpen, WishStatusReached, WishStatusRejected, WishStatusConnected:
		now := time.Now().UTC()
		if _, err := s.db.ExecContext(ctx, `UPDATE wish_sites SET status = ?, resolved_at = CASE WHEN ? IN (?, ?, ?) THEN ? ELSE resolved_at END WHERE id = ?`,
			status, status, WishStatusReached, WishStatusRejected, WishStatusConnected, unixMilli(now), id); err != nil {
			return WishSite{}, err
		}
	default:
		return WishSite{}, errors.New("未知状态")
	}
	return s.GetWishSite(ctx, id)
}

func (s *Store) DeleteWishSite(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM wish_sites WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
