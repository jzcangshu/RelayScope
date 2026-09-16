package store

import (
	"context"
	"errors"
	"time"
)

const (
	OrderKindWish       = "wish"
	OrderKindMembership = "membership"

	OrderStatusPending   = "pending"
	OrderStatusPaid      = "paid"
	OrderStatusRefunded  = "refunded"
	OrderStatusCancelled = "cancelled"

	OrderFundingLDC    = "ldc"
	OrderFundingCredit = "credit"
)

type LDCOrder struct {
	ID              int64      `json:"id"`
	OrderNo         string     `json:"orderNo"`
	UserID          int64      `json:"userId"`
	Kind            string     `json:"kind"`
	WishSiteID      *int64     `json:"wishSiteId"`
	Days            *int64     `json:"days"`
	AmountLDC       int64      `json:"amountLdc"`
	Funding         string     `json:"funding"`
	Status          string     `json:"status"`
	PlatformTradeNo string     `json:"platformTradeNo"`
	Username        string     `json:"username,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	PaidAt          *time.Time `json:"paidAt"`
}

func (s *Store) CreateOrder(ctx context.Context, order LDCOrder) (LDCOrder, error) {
	if order.UserID <= 0 || order.AmountLDC <= 0 || order.AmountLDC > 1_000_000 {
		return LDCOrder{}, errors.New("invalid order")
	}
	switch order.Kind {
	case OrderKindWish:
		if order.WishSiteID == nil || *order.WishSiteID <= 0 || order.Days != nil {
			return LDCOrder{}, errors.New("invalid wish order")
		}
	case OrderKindMembership:
		if order.Days == nil || *order.Days <= 0 || *order.Days > 3650 || order.WishSiteID != nil {
			return LDCOrder{}, errors.New("invalid membership order")
		}
	default:
		return LDCOrder{}, errors.New("invalid order kind")
	}
	now := time.Now().UTC()
	funding := order.Funding
	if funding != OrderFundingCredit {
		funding = OrderFundingLDC
	}
	order.Funding = funding
	result, err := s.db.ExecContext(ctx, `INSERT INTO ldc_orders(order_no, user_id, kind, wish_site_id, days, amount_ldc, funding, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		order.OrderNo, order.UserID, order.Kind, order.WishSiteID, order.Days, order.AmountLDC, funding, OrderStatusPending, unixMilli(now))
	if err != nil {
		return LDCOrder{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return LDCOrder{}, err
	}
	order.ID = id
	order.Status = OrderStatusPending
	order.CreatedAt = now
	return order, nil
}

func (s *Store) GetOrder(ctx context.Context, id int64) (LDCOrder, error) {
	var o LDCOrder
	var wishID, days *int64
	var created int64
	var paidAt *int64
	err := s.db.QueryRowContext(ctx, `SELECT id, order_no, user_id, kind, wish_site_id, days, amount_ldc, funding, status, platform_trade_no, created_at, paid_at FROM ldc_orders WHERE id = ?`, id).
		Scan(&o.ID, &o.OrderNo, &o.UserID, &o.Kind, &wishID, &days, &o.AmountLDC, &o.Funding, &o.Status, &o.PlatformTradeNo, &created, &paidAt)
	if err != nil {
		return LDCOrder{}, err
	}
	o.WishSiteID = wishID
	o.Days = days
	o.CreatedAt = time.UnixMilli(created).UTC()
	if paidAt != nil {
		at := time.UnixMilli(*paidAt).UTC()
		o.PaidAt = &at
	}
	return o, nil
}

func (s *Store) GetOrderByNo(ctx context.Context, orderNo string) (LDCOrder, error) {
	var o LDCOrder
	var wishID, days *int64
	var created int64
	var paidAt *int64
	err := s.db.QueryRowContext(ctx, `SELECT id, order_no, user_id, kind, wish_site_id, days, amount_ldc, funding, status, platform_trade_no, created_at, paid_at FROM ldc_orders WHERE order_no = ?`, orderNo).
		Scan(&o.ID, &o.OrderNo, &o.UserID, &o.Kind, &wishID, &days, &o.AmountLDC, &o.Funding, &o.Status, &o.PlatformTradeNo, &created, &paidAt)
	if err != nil {
		return LDCOrder{}, err
	}
	o.WishSiteID = wishID
	o.Days = days
	o.CreatedAt = time.UnixMilli(created).UTC()
	if paidAt != nil {
		at := time.UnixMilli(*paidAt).UTC()
		o.PaidAt = &at
	}
	return o, nil
}

// MarkOrderPaid 把 pending 订单落为 paid，并同步触发会员延期 / 许愿达标判定。
// 返回 changed=false 表示订单此前已终态（回调重放幂等）。
func (s *Store) MarkOrderPaid(ctx context.Context, orderNo, platformTradeNo string) (LDCOrder, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LDCOrder{}, false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE ldc_orders SET status = ?, platform_trade_no = ?, paid_at = ? WHERE order_no = ? AND status = ?`,
		OrderStatusPaid, platformTradeNo, unixMilli(now), orderNo, OrderStatusPending)
	if err != nil {
		return LDCOrder{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return LDCOrder{}, false, tx.Commit()
	}
	var order LDCOrder
	var wishID, days *int64
	if err := tx.QueryRowContext(ctx, `SELECT id, order_no, user_id, kind, wish_site_id, days, amount_ldc, funding, status FROM ldc_orders WHERE order_no = ?`, orderNo).
		Scan(&order.ID, &order.OrderNo, &order.UserID, &order.Kind, &wishID, &days, &order.AmountLDC, &order.Funding, &order.Status); err != nil {
		return LDCOrder{}, false, err
	}
	order.WishSiteID = wishID
	order.Days = days
	switch order.Kind {
	case OrderKindMembership:
		if _, err := tx.ExecContext(ctx, `UPDATE users SET membership_expires_at = MAX(COALESCE(membership_expires_at, 0), ?) + ?, updated_at = ? WHERE id = ?`,
			unixMilli(now), *order.Days*dayMillis, unixMilli(now), order.UserID); err != nil {
			return LDCOrder{}, false, err
		}
	case OrderKindWish:
		// 支付到账后判定许愿目标是否达成（target_ldc 为 NULL 表示目标未定，永不触发）
		if _, err := tx.ExecContext(ctx, `UPDATE wish_sites SET status = ?, resolved_at = ?
			WHERE id = ? AND status = ? AND target_ldc IS NOT NULL
			AND target_ldc <= (SELECT COALESCE(SUM(amount_ldc), 0) FROM ldc_orders WHERE wish_site_id = wish_sites.id AND status = ?)`,
			WishStatusReached, unixMilli(now), *order.WishSiteID, WishStatusOpen, OrderStatusPaid); err != nil {
			return LDCOrder{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LDCOrder{}, false, err
	}
	order.Status = OrderStatusPaid
	order.PlatformTradeNo = platformTradeNo
	order.PaidAt = &now
	return order, true, nil
}

// MarkOrderRefunded 管理端登记退款：进度随 SUM 自然回落；若因此跌破目标则回到 open。
func (s *Store) MarkOrderRefunded(ctx context.Context, orderID int64) (LDCOrder, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LDCOrder{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE ldc_orders SET status = ? WHERE id = ? AND status = ?`, OrderStatusRefunded, orderID, OrderStatusPaid)
	if err != nil {
		return LDCOrder{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return LDCOrder{}, errors.New("订单不存在或不在可退款状态")
	}
	var order LDCOrder
	var wishID, days *int64
	if err := tx.QueryRowContext(ctx, `SELECT id, order_no, user_id, kind, wish_site_id, days, amount_ldc, funding, status FROM ldc_orders WHERE id = ?`, orderID).
		Scan(&order.ID, &order.OrderNo, &order.UserID, &order.Kind, &wishID, &days, &order.AmountLDC, &order.Funding, &order.Status); err != nil {
		return LDCOrder{}, err
	}
	if wishID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE wish_sites SET status = ?, resolved_at = NULL
			WHERE id = ? AND status = ? AND target_ldc IS NOT NULL
			AND target_ldc > (SELECT COALESCE(SUM(amount_ldc), 0) FROM ldc_orders WHERE wish_site_id = wish_sites.id AND status = ?)`,
			WishStatusOpen, *wishID, WishStatusReached, OrderStatusPaid); err != nil {
			return LDCOrder{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LDCOrder{}, err
	}
	order.Status = OrderStatusRefunded
	return order, nil
}

func (s *Store) CancelExpiredOrders(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE ldc_orders SET status = ? WHERE status = ? AND created_at < ?`,
		OrderStatusCancelled, OrderStatusPending, unixMilli(cutoff))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CancelOrder 作废单个待支付订单（如平台下单失败时）。
func (s *Store) CancelOrder(ctx context.Context, orderNo string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ldc_orders SET status = ? WHERE order_no = ? AND status = ?`,
		OrderStatusCancelled, orderNo, OrderStatusPending)
	return err
}

// ListOrders 管理端订单流水。userID>0 按用户过滤，kind/status 空串不过滤。
func (s *Store) ListOrders(ctx context.Context, userID int64, kind, status string, limit int) ([]LDCOrder, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT o.id, o.order_no, o.user_id, o.kind, o.wish_site_id, o.days, o.amount_ldc, o.funding, o.status, o.platform_trade_no, o.created_at, o.paid_at, u.username
		FROM ldc_orders o JOIN users u ON u.id = o.user_id WHERE 1 = 1`
	args := []any{}
	if userID > 0 {
		query += ` AND o.user_id = ?`
		args = append(args, userID)
	}
	if kind != "" {
		query += ` AND o.kind = ?`
		args = append(args, kind)
	}
	if status != "" {
		query += ` AND o.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY o.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LDCOrder{}
	for rows.Next() {
		var o LDCOrder
		var wishID, days *int64
		var created int64
		var paidAt *int64
		if err := rows.Scan(&o.ID, &o.OrderNo, &o.UserID, &o.Kind, &wishID, &days, &o.AmountLDC, &o.Funding, &o.Status, &o.PlatformTradeNo, &created, &paidAt, &o.Username); err != nil {
			return nil, err
		}
		o.WishSiteID = wishID
		o.Days = days
		o.CreatedAt = time.UnixMilli(created).UTC()
		if paidAt != nil {
			at := time.UnixMilli(*paidAt).UTC()
			o.PaidAt = &at
		}
		items = append(items, o)
	}
	return items, rows.Err()
}
