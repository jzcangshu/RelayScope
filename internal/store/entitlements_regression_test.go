package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCreditPledgeRollsBackOnOrderFailure(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "credit", "credit", "", "", 1)
	db.ExtendMembership(ctx, user.ID, 30)
	wish, err := db.CreateWishSite(ctx, user.ID, "test", "https://credit.example.com", false, 30)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := db.PledgeMonthlyCredit(ctx, user.ID, wish.ID, "same", 10, 30, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PledgeMonthlyCredit(ctx, user.ID, wish.ID, "same", 10, 30, now); err == nil {
		t.Fatal("duplicate should fail")
	}
	credit, _, _ := db.GetMonthlyCredit(ctx, user.ID, now)
	if credit.UsedLDC != 10 {
		t.Fatalf("failed order consumed credit: %+v", credit)
	}
	if used, err := db.PledgeMonthlyCredit(ctx, user.ID, wish.ID, "next", 50, 30, now); err != nil || used != 20 {
		t.Fatalf("used %d: %v", used, err)
	}
	site, _ := db.GetWishSite(ctx, wish.ID)
	if site.Status != WishStatusReached {
		t.Fatalf("not reached: %+v", site)
	}
}

func TestMembershipRefundPreservesLaterGiftAndRenewal(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "refund", "refund", "", "", 1)
	days := int64(30)
	first, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "first", UserID: user.ID, Kind: OrderKindMembership, Days: &days, AmountLDC: 30})
	if _, _, err := db.MarkOrderPaid(ctx, "first", "trade"); err != nil {
		t.Fatal(err)
	}
	db.ExtendMembership(ctx, user.ID, 7)
	db.CreateOrder(ctx, LDCOrder{OrderNo: "second", UserID: user.ID, Kind: OrderKindMembership, Days: &days, AmountLDC: 30})
	db.MarkOrderPaid(ctx, "second", "trade2")
	before, _ := db.GetMembership(ctx, user.ID)
	if _, err := db.MarkOrderRefunded(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetMembership(ctx, user.ID)
	delta := before.ExpiresAt.Sub(*after.ExpiresAt)
	if delta < 29*24*time.Hour || delta > 30*24*time.Hour {
		t.Fatalf("refund removed %v", delta)
	}
	if time.Until(*after.ExpiresAt) < 36*24*time.Hour {
		t.Fatal("gift or renewal lost")
	}
	db.MarkOrderRefunded(ctx, first.ID)
	replay, _ := db.GetMembership(ctx, user.ID)
	if !after.ExpiresAt.Equal(*replay.ExpiresAt) {
		t.Fatal("replay revoked twice")
	}
}

func TestMembershipRefundOnlyRemainingInterval(t *testing.T) {
	for _, consumedDays := range []int64{10, 40} {
		t.Run(time.Duration(consumedDays).String(), func(t *testing.T) {
			db := newTestStore(t)
			ctx := context.Background()
			now := time.Now().UTC()
			user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "partial", "partial", "", "", 1)
			order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "partial", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
			db.MarkOrderPaid(ctx, order.OrderNo, "trade")
			start := unixMilli(now) - consumedDays*dayMillis
			end := start + 30*dayMillis
			db.db.Exec(`UPDATE membership_order_intervals SET starts_at=?, ends_at=? WHERE order_id=?`, start, end, order.ID)
			db.db.Exec(`UPDATE users SET membership_expires_at=? WHERE id=?`, end+50*dayMillis, user.ID)
			tx, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := revokeOrderMembership(ctx, tx, order, now); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			got, _ := db.GetMembership(ctx, user.ID)
			want := end + 50*dayMillis - max(int64(0), 30-consumedDays)*dayMillis
			if got.ExpiresAt.UnixMilli() != want {
				t.Fatalf("expiry %v want %v", got.ExpiresAt, time.UnixMilli(want))
			}
		})
	}
}

func TestMembershipExpiryAdjustmentDoesNotResurrectRemovedOrder(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "adjust", "adjust", "", "", 1)
	order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "adjust", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	db.MarkOrderPaid(ctx, order.OrderNo, "trade")
	short := time.Now().UTC().Add(5 * 24 * time.Hour)
	if _, err := db.SetMembershipByUsername(ctx, ProviderLinuxDO, user.Username, &short); err != nil {
		t.Fatal(err)
	}
	db.ExtendMembership(ctx, user.ID, 7)
	if _, err := db.MarkOrderRefunded(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMembership(ctx, user.ID)
	if left := time.Until(*got.ExpiresAt); left < 6*24*time.Hour || left > 8*24*time.Hour {
		t.Fatalf("gift lost: %v", left)
	}
}

func TestLegacyMembershipRefundDoesNotChangeStatus(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "old", "old", "", "", 1)
	order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "old", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	db.db.Exec(`UPDATE ldc_orders SET status='paid' WHERE id=?`, order.ID)
	if err := db.ValidateOrderRefund(ctx, order.ID); err != ErrLegacyMembershipRefund {
		t.Fatalf("got %v", err)
	}
	if _, err := db.MarkOrderRefunded(ctx, order.ID); err != ErrLegacyMembershipRefund {
		t.Fatalf("got %v", err)
	}
	got, _ := db.GetOrder(ctx, order.ID)
	if got.Status != OrderStatusPaid {
		t.Fatal("failed refund changed status")
	}
}

func TestCreditPledgesConcurrentNeverLoseDebitedAmount(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "concurrent", "concurrent", "", "", 1)
	db.ExtendMembership(ctx, user.ID, 30)
	wish, _ := db.CreateWishSite(ctx, user.ID, "concurrent", "https://concurrent.example.com", false, 1000)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.PledgeMonthlyCredit(ctx, user.ID, wish.ID, fmt.Sprint("credit-", i), 7, 30, time.Now())
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	credit, _, _ := db.GetMonthlyCredit(ctx, user.ID, time.Now())
	var total int64
	db.db.QueryRow(`SELECT COALESCE(SUM(amount_ldc),0) FROM ldc_orders WHERE user_id=? AND status='paid'`, user.ID).Scan(&total)
	if total != 30 || credit.UsedLDC != total {
		t.Fatalf("paid %d debit %d", total, credit.UsedLDC)
	}
}

func TestConcurrentMembershipRefundRevokesOnce(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "twice", "twice", "", "", 1)
	order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "twice", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	db.MarkOrderPaid(ctx, order.OrderNo, "trade")
	db.ExtendMembership(ctx, user.ID, 7)
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := db.MarkOrderRefunded(ctx, order.ID); success <- err == nil }()
	}
	wg.Wait()
	close(success)
	count := 0
	for ok := range success {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("refund successes %d", count)
	}
	got, _ := db.GetMembership(ctx, user.ID)
	if left := time.Until(*got.ExpiresAt); left < 6*24*time.Hour || left > 8*24*time.Hour {
		t.Fatalf("remaining %v", left)
	}
}

func TestPlatformRefundClaimBlocksReplayAndAllowsExplicitRejectionRetry(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "lock", "lock", "", "", 1)
	order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "lock", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	db.MarkOrderPaid(ctx, order.OrderNo, "trade")
	var wg sync.WaitGroup
	successes := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); successes <- db.ClaimPlatformRefund(ctx, order.ID) == nil }()
	}
	wg.Wait()
	close(successes)
	n := 0
	for ok := range successes {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("claims %d", n)
	}
	if err := db.RecordPlatformRefundResult(ctx, order.ID, "rejected"); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimPlatformRefund(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	db.RecordPlatformRefundResult(ctx, order.ID, "uncertain")
	if err := db.ClaimPlatformRefund(ctx, order.ID); err == nil {
		t.Fatal("uncertain transfer retried")
	}
	// Confirmed external success can still be reconciled locally without another transfer.
	if _, err := db.MarkOrderRefunded(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCreditPledgeRollsBackWhenGoalUpdateFails(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	user, _ := db.UpsertUser(ctx, ProviderLinuxDO, "goal", "goal", "", "", 1)
	db.ExtendMembership(ctx, user.ID, 30)
	wish, _ := db.CreateWishSite(ctx, user.ID, "goal", "https://goal.example.com", false, 10)
	if _, err := db.db.Exec(`CREATE TRIGGER fail_goal BEFORE UPDATE ON wish_sites BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PledgeMonthlyCredit(ctx, user.ID, wish.ID, "goal", 10, 30, now); err == nil {
		t.Fatal("injected failure not reached")
	}
	if _, exists, err := db.GetMonthlyCredit(ctx, user.ID, now); err != nil || exists {
		t.Fatalf("grant/debit leaked: exists=%v err=%v", exists, err)
	}
	orders, err := db.ListOrders(ctx, user.ID, "", "", 100)
	if err != nil || len(orders) != 0 {
		t.Fatalf("paid order leaked: %v %v", orders, err)
	}
}

func TestRefundPreservesRedeemAndPreregistrationGifts(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	pre := time.Now().UTC().Add(7 * 24 * time.Hour)
	if _, err := db.SetMembershipByUsername(ctx, ProviderLinuxDO, "gifts", &pre); err != nil {
		t.Fatal(err)
	}
	user, err := db.UpsertUser(ctx, ProviderLinuxDO, "gifts", "gifts", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	order, _ := db.CreateOrder(ctx, LDCOrder{OrderNo: "gifts", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	db.MarkOrderPaid(ctx, order.OrderNo, "trade")
	codes, err := db.GenerateRedeemCodes(ctx, 1, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemCode(ctx, user.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkOrderRefunded(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMembership(ctx, user.ID)
	if got.ExpiresAt.UnixMilli() != pre.UnixMilli()+5*dayMillis {
		t.Fatalf("gift duration altered: %v", got.ExpiresAt)
	}
}
