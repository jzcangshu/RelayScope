package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 每月免费许愿额度：惰性发放——会员当月首次使用时自动入账，
// (user_id, period) 唯一约束保证每人每月只发一次，无需定时任务。

type WishCredit struct {
	UserID     int64     `json:"userId"`
	Period     string    `json:"period"`
	GrantedLDC int64     `json:"grantedLdc"`
	UsedLDC    int64     `json:"usedLdc"`
	CreatedAt  time.Time `json:"createdAt"`
}

func currentCreditPeriod(now time.Time) string {
	return now.UTC().Format("2006-01")
}

// EnsureMonthlyCredit 确保用户当月额度已发放（发放额度由调用方按运营设置给定），
// 返回当月剩余可用额度。非会员不发放，调用方自行把关会员状态。
func (s *Store) EnsureMonthlyCredit(ctx context.Context, userID int64, grantLDC int64, now time.Time) (int64, error) {
	if userID <= 0 || grantLDC <= 0 {
		return 0, errors.New("invalid credit grant")
	}
	period := currentCreditPeriod(now)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO wish_credits(user_id, period, granted_ldc, used_ldc, created_at) VALUES (?, ?, ?, 0, ?)
		ON CONFLICT(user_id, period) DO NOTHING`, userID, period, grantLDC, unixMilli(now)); err != nil {
		return 0, err
	}
	return s.availableCredit(ctx, userID, period)
}

// ConsumeMonthlyCredit 消耗当月免费额度（自动截断到可用余额），返回实际消耗量。
// 单条 UPDATE 带 CHECK(used_ldc <= granted_ldc) 兜底，并发下不会超发。
func (s *Store) ConsumeMonthlyCredit(ctx context.Context, userID int64, amount int64, now time.Time) (int64, error) {
	if userID <= 0 || amount <= 0 {
		return 0, nil
	}
	period := currentCreditPeriod(now)
	var granted, used int64
	err := s.db.QueryRowContext(ctx, `SELECT granted_ldc, used_ldc FROM wish_credits WHERE user_id = ? AND period = ?`, userID, period).Scan(&granted, &used)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	consumed := granted - used
	if consumed > amount {
		consumed = amount
	}
	if consumed <= 0 {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE wish_credits SET used_ldc = used_ldc + ? WHERE user_id = ? AND period = ? AND used_ldc + ? <= granted_ldc`,
		consumed, userID, period, consumed)
	if err != nil {
		return 0, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return 0, nil
	}
	return consumed, nil
}

// GetMonthlyCredit 只读查询当月额度（不发放），无记录返回零值。
func (s *Store) GetMonthlyCredit(ctx context.Context, userID int64, now time.Time) (WishCredit, bool, error) {
	var credit WishCredit
	var created int64
	period := currentCreditPeriod(now)
	err := s.db.QueryRowContext(ctx, `SELECT user_id, period, granted_ldc, used_ldc, created_at FROM wish_credits WHERE user_id = ? AND period = ?`, userID, period).
		Scan(&credit.UserID, &credit.Period, &credit.GrantedLDC, &credit.UsedLDC, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WishCredit{}, false, nil
		}
		return WishCredit{}, false, err
	}
	credit.CreatedAt = time.UnixMilli(created).UTC()
	return credit, true, nil
}

func (s *Store) availableCredit(ctx context.Context, userID int64, period string) (int64, error) {
	var granted, used int64
	err := s.db.QueryRowContext(ctx, `SELECT granted_ldc, used_ldc FROM wish_credits WHERE user_id = ? AND period = ?`, userID, period).Scan(&granted, &used)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return granted - used, nil
}
