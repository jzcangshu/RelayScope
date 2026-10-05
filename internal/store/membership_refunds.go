package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrLegacyMembershipRefund = errors.New("历史会员订单不支持自动退款；仅升级后支付的订单可自动撤销剩余权益")

// ValidateOrderRefund must run before any external money movement.
func (s *Store) ValidateOrderRefund(ctx context.Context, id int64) error {
	var kind, status string
	var start, end sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT o.kind, o.status, i.starts_at, i.ends_at FROM ldc_orders o LEFT JOIN membership_order_intervals i ON i.order_id = o.id WHERE o.id = ?`, id).Scan(&kind, &status, &start, &end); err != nil {
		return err
	}
	if status != OrderStatusPaid {
		return errors.New("订单不存在或不在可退款状态")
	}
	if kind == OrderKindMembership && (!start.Valid || !end.Valid) {
		return ErrLegacyMembershipRefund
	}
	return nil
}

// Paid grants own a time interval; unowned gaps represent gifts or legacy balances.
// Closing a refunded interval shifts subsequent grants without consuming those gaps.
func revokeOrderMembership(ctx context.Context, tx *sql.Tx, order LDCOrder, now time.Time) error {
	var start, end sql.NullInt64
	var expiry int64
	if err := tx.QueryRowContext(ctx, `SELECT starts_at, ends_at FROM membership_order_intervals WHERE order_id = ?`, order.ID).Scan(&start, &end); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLegacyMembershipRefund
		}
		return err
	}
	if !start.Valid || !end.Valid {
		return ErrLegacyMembershipRefund
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(membership_expires_at,0) FROM users WHERE id = ?`, order.UserID).Scan(&expiry); err != nil {
		return err
	}
	from := max(start.Int64, unixMilli(now))
	until := min(end.Int64, expiry)
	remaining := max(int64(0), until-from)
	if remaining == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET membership_expires_at = membership_expires_at - ?, updated_at = ? WHERE id = ?`, remaining, unixMilli(now), order.UserID); err != nil {
		return err
	}
	// Intervals may have been clipped by an administrative expiry adjustment.
	if _, err := tx.ExecContext(ctx, `UPDATE membership_order_intervals SET starts_at = starts_at - ?, ends_at = ends_at - ? WHERE user_id = ? AND order_id <> ? AND starts_at >= ?`, remaining, remaining, order.UserID, order.ID, until); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE membership_order_intervals SET ends_at = ? WHERE order_id = ?`, min(end.Int64, from), order.ID)
	return err
}

// Explicit expiry edits remove the tail first. Extending later cannot resurrect
// an order's already removed contribution.
func clipMembershipIntervals(ctx context.Context, tx *sql.Tx, userID int64, expiry any) error {
	_, err := tx.ExecContext(ctx, `UPDATE membership_order_intervals SET starts_at = MIN(starts_at, COALESCE(?,0)), ends_at = MIN(ends_at, COALESCE(?,0)) WHERE user_id = ? AND ends_at > COALESCE(?,0)`, expiry, expiry, userID, expiry)
	return err
}

// ClaimPlatformRefund is durable: a process crash must not enable a second transfer.
func (s *Store) ClaimPlatformRefund(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO order_refund_attempts(order_id,status,updated_at)
 SELECT id,'running',? FROM ldc_orders WHERE id=? AND status='paid' AND funding='ldc'
 ON CONFLICT(order_id) DO UPDATE SET status='running',updated_at=excluded.updated_at WHERE order_refund_attempts.status='rejected'`, unixMilli(time.Now()), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("该订单已有平台退款记录或正在处理；请核对平台结果，确认已退款后仅登记本地状态")
	}
	return nil
}

func (s *Store) RecordPlatformRefundResult(ctx context.Context, id int64, status string) error {
	if status != "succeeded" && status != "uncertain" && status != "rejected" {
		return errors.New("invalid refund result")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE order_refund_attempts SET status=?,updated_at=? WHERE order_id=? AND status='running'`, status, unixMilli(time.Now()), id)
	return err
}
