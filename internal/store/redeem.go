package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// 兑换码字符表：Crockford 风格，去掉易混淆的 I/L/O/U，12 字符 = 60 bit 熵。
const redeemAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	RedeemStatusUnused  = "unused"
	RedeemStatusUsed    = "used"
	RedeemStatusRevoked = "revoked"
)

var ErrRedeemCodeInvalid = errors.New("兑换码无效或已被使用")

type RedeemCode struct {
	ID            int64      `json:"id"`
	Code          string     `json:"code"`
	Days          int64      `json:"days"`
	Note          string     `json:"note"`
	Status        string     `json:"status"`
	RedeemedBy    int64      `json:"redeemedBy"`
	RedeemedByName string    `json:"redeemedByName"`
	RedeemedAt    *time.Time `json:"redeemedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func randomRedeemCode() (display, canonical string, err error) {
	raw := make([]byte, 12)
	for i := range raw {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(redeemAlphabet))))
		if err != nil {
			return "", "", err
		}
		raw[i] = redeemAlphabet[n.Int64()]
	}
	display = fmt.Sprintf("RS-%s-%s-%s", raw[0:4], raw[4:8], raw[8:12])
	canonical = "RS" + string(raw)
	return display, canonical, nil
}

// normalizeRedeemCode 抹平用户输入差异：去分隔符、大写、易混淆字符归位，得到库内规范形态。
func normalizeRedeemCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	replacer := strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1")
	return replacer.Replace(code)
}

// FormatRedeemCode 把库内规范形态渲染为带连字符的展示形态。
func FormatRedeemCode(canonical string) string {
	if len(canonical) != 14 || !strings.HasPrefix(canonical, "RS") {
		return canonical
	}
	return "RS-" + canonical[2:6] + "-" + canonical[6:10] + "-" + canonical[10:14]
}

func (s *Store) GenerateRedeemCodes(ctx context.Context, count, days int64, note string) ([]string, error) {
	if count <= 0 || count > 500 {
		return nil, errors.New("单批数量需在 1-500 之间")
	}
	if days <= 0 || days > 3650 {
		return nil, errors.New("会员天数需在 1-3650 之间")
	}
	if len(note) > 200 {
		return nil, errors.New("备注过长")
	}
	codes := make([]string, 0, count)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := unixMilli(time.Now().UTC())
	for len(codes) < int(count) {
		display, canonical, err := randomRedeemCode()
		if err != nil {
			return nil, err
		}
		// 60 bit 熵下冲突几乎不可能；真撞了重抽即可。
		if _, err := tx.ExecContext(ctx, `INSERT INTO redeem_codes(code, days, note, status, created_at) VALUES (?, ?, ?, ?, ?)`, canonical, days, note, RedeemStatusUnused, now); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return nil, err
		}
		codes = append(codes, display)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Store) ListRedeemCodes(ctx context.Context, status string, limit, offset int) ([]RedeemCode, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT c.id, c.code, c.days, c.note, c.status, COALESCE(c.redeemed_by, 0), COALESCE(u.username, ''), c.redeemed_at, c.created_at
		FROM redeem_codes c LEFT JOIN users u ON u.id = c.redeemed_by`
	args := []any{}
	if status != "" {
		query += ` WHERE c.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY c.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RedeemCode, 0, limit)
	for rows.Next() {
		var c RedeemCode
		var redeemedAt *int64
		var created int64
		if err := rows.Scan(&c.ID, &c.Code, &c.Days, &c.Note, &c.Status, &c.RedeemedBy, &c.RedeemedByName, &redeemedAt, &created); err != nil {
			return nil, err
		}
		if redeemedAt != nil {
			at := time.UnixMilli(*redeemedAt).UTC()
			c.RedeemedAt = &at
		}
		c.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Store) RevokeRedeemCodes(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return 0, errors.New("需要 1-500 个兑换码 ID")
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE redeem_codes SET status = ? WHERE status = ? AND id IN (`+placeholders+`)`, append([]any{RedeemStatusRevoked, RedeemStatusUnused}, args...)...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RedeemCode 核销兑换码并延长会员，整体在一个事务内完成；并发核销同一码只有一个成功。
func (s *Store) RedeemCode(ctx context.Context, userID int64, code string) (Membership, error) {
	normalized := normalizeRedeemCode(code)
	if userID <= 0 || len(normalized) < 8 || len(normalized) > 40 {
		return Membership{}, ErrRedeemCodeInvalid
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE redeem_codes SET status = ?, redeemed_by = ?, redeemed_at = ? WHERE code = ? AND status = ?`,
		RedeemStatusUsed, userID, unixMilli(now), normalized, RedeemStatusUnused)
	if err != nil {
		return Membership{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return Membership{}, ErrRedeemCodeInvalid
	}
	var days int64
	if err := tx.QueryRowContext(ctx, `SELECT days FROM redeem_codes WHERE code = ?`, normalized).Scan(&days); err != nil {
		return Membership{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET membership_expires_at = MAX(COALESCE(membership_expires_at, 0), ?) + ?, updated_at = ? WHERE id = ?`,
		unixMilli(now), days*dayMillis, unixMilli(now), userID); err != nil {
		return Membership{}, err
	}
	var expires *int64
	if err := tx.QueryRowContext(ctx, `SELECT membership_expires_at FROM users WHERE id = ?`, userID).Scan(&expires); err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, err
	}
	return membershipFromMillis(expires, now), nil
}

// EqualRedeemStatus 供需要对比状态的调用方使用（常量时间比较，防时序侧信道纯属仪式感）。
func EqualRedeemStatus(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
