//go:build integration

// These tests need a real Postgres. They are behind a build tag rather than
// testing.Short() because -short is opt-out: a teammate running `go test ./...`
// without a database would get failures. With the tag, the default run does not
// even compile this file.
//
//	docker compose up -d db
//	go test -tags=integration ./internal/repository/... -v -count=1
package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"wallet-service/internal/config"
	"wallet-service/internal/domain"
	"wallet-service/internal/service"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- fixtures ---

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	cfg := config.Load()
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Skipf("postgres not reachable (%v); start it with: docker compose up -d db", err)
	}
	if err := db.AutoMigrate(&domain.Wallet{}, &domain.Transaction{}); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	return db
}

func newTestService(t *testing.T) (service.WalletService, domain.Repository, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	return service.NewWalletService(repo), repo, db
}

// makeWallet creates a wallet with a unique user id so tests never collide and
// can be re-run without truncating anything.
func makeWallet(t *testing.T, svc service.WalletService, balance int64, currency string) *domain.Wallet {
	t.Helper()
	ctx := context.Background()

	w, err := svc.CreateWallet(ctx, "test-"+uuid.NewString(), currency)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if balance > 0 {
		if _, err := svc.Deposit(ctx, w.ID, balance, "seed", nil); err != nil {
			t.Fatalf("seed deposit: %v", err)
		}
	}
	return w
}

func balanceOf(t *testing.T, svc service.WalletService, id uuid.UUID) int64 {
	t.Helper()
	w, err := svc.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	return w.Balance
}

// assertWalletConsistent checks the invariant that ties the ledger to the
// balance: the stored balance must equal everything the history says happened.
// If any test ever loses, duplicates or misses an entry, this catches it.
//
//	balance == SUM(deposits in) - SUM(withdrawals out)
//	           + SUM(transfers in) - SUM(transfers out)
func assertWalletConsistent(t *testing.T, db *gorm.DB, walletID uuid.UUID) {
	t.Helper()

	var derived int64
	err := db.Raw(`
		SELECT COALESCE(SUM(
			CASE
				WHEN to_wallet_id   = ? THEN amount
				WHEN from_wallet_id = ? THEN -amount
				ELSE 0
			END
		), 0)
		FROM transactions
		WHERE from_wallet_id = ? OR to_wallet_id = ?`,
		walletID, walletID, walletID, walletID).Scan(&derived).Error
	if err != nil {
		t.Fatalf("invariant query failed: %v", err)
	}

	var w domain.Wallet
	if err := db.First(&w, "id = ?", walletID).Error; err != nil {
		t.Fatalf("load wallet: %v", err)
	}

	if w.Balance != derived {
		t.Errorf("balance %d does not match the transaction history (%d) for wallet %s",
			w.Balance, derived, walletID)
	}
}

func countTransactions(t *testing.T, db *gorm.DB, walletID uuid.UUID) int64 {
	t.Helper()
	var n int64
	err := db.Model(&domain.Transaction{}).
		Where("from_wallet_id = ? OR to_wallet_id = ?", walletID, walletID).
		Count(&n).Error
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return n
}

// --- 1. concurrency: no lost update ---

// Without SELECT ... FOR UPDATE this fails: concurrent transfers read the same
// balance, and one of the debits is silently overwritten by the other.
func TestConcurrentTransfers_NoLostUpdate(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	from := makeWallet(t, svc, 100_000, "THB")
	to := makeWallet(t, svc, 0, "THB")

	const workers = 100
	const amount = 100

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Transfer(ctx, from.ID, to.ID, amount, "concurrent", nil); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("transfer failed: %v", err)
	}

	gotFrom, gotTo := balanceOf(t, svc, from.ID), balanceOf(t, svc, to.ID)
	if gotFrom != 90_000 {
		t.Errorf("sender = %d, want 90000", gotFrom)
	}
	if gotTo != 10_000 {
		t.Errorf("recipient = %d, want 10000", gotTo)
	}
	if gotFrom+gotTo != 100_000 {
		t.Errorf("total = %d, want 100000: money was created or destroyed", gotFrom+gotTo)
	}

	// One seed deposit plus one row per transfer.
	if n := countTransactions(t, db, to.ID); n != workers {
		t.Errorf("recipient has %d transactions, want %d", n, workers)
	}

	assertWalletConsistent(t, db, from.ID)
	assertWalletConsistent(t, db, to.ID)
}

// --- 2. concurrency: no deadlock ---

// Locking sender-then-receiver deadlocks here: A->B holds A and wants B while
// B->A holds B and wants A, and Postgres kills one with SQLSTATE 40P01.
// Sorting the lock order by wallet UUID removes the cycle.
func TestBidirectionalTransfers_NoDeadlock(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	a := makeWallet(t, svc, 50_000, "THB")
	b := makeWallet(t, svc, 50_000, "THB")

	const perDirection = 50
	const amount = 100

	var wg sync.WaitGroup
	errCh := make(chan error, perDirection*2)

	for i := 0; i < perDirection; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := svc.Transfer(ctx, a.ID, b.ID, amount, "a->b", nil); err != nil {
				errCh <- fmt.Errorf("a->b: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := svc.Transfer(ctx, b.ID, a.ID, amount, "b->a", nil); err != nil {
				errCh <- fmt.Errorf("b->a: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("transfer failed (a deadlock would appear here as SQLSTATE 40P01): %v", err)
	}

	gotA, gotB := balanceOf(t, svc, a.ID), balanceOf(t, svc, b.ID)
	if gotA+gotB != 100_000 {
		t.Errorf("total = %d, want 100000", gotA+gotB)
	}

	assertWalletConsistent(t, db, a.ID)
	assertWalletConsistent(t, db, b.ID)
}

// --- 3. concurrency: no overdraft ---

// The plainest statement of correctness: a wallet cannot be overdrawn even
// when every withdrawal is issued at the same instant.
func TestConcurrentWithdraw_NoOverdraft(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	w := makeWallet(t, svc, 1_000, "THB")

	const workers = 20
	const amount = 100 // exactly 10 of these fit in the balance

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, rejected := 0, 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Withdraw(ctx, w.ID, amount, "race", nil)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrInsufficientBalance):
				rejected++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if succeeded != 10 {
		t.Errorf("%d withdrawals succeeded, want exactly 10", succeeded)
	}
	if rejected != 10 {
		t.Errorf("%d withdrawals were rejected, want exactly 10", rejected)
	}

	final := balanceOf(t, svc, w.ID)
	if final != 0 {
		t.Errorf("final balance = %d, want 0", final)
	}
	if final < 0 {
		t.Errorf("balance went negative: %d", final)
	}

	assertWalletConsistent(t, db, w.ID)
}

// --- 4. failure mid-transfer leaves no partial state ---

func TestTransferFails_NoPartialState(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func(t *testing.T, svc service.WalletService) (from, to uuid.UUID)
		amount  int64
		wantErr error
	}{
		{
			name: "recipient does not exist",
			setup: func(t *testing.T, svc service.WalletService) (uuid.UUID, uuid.UUID) {
				return makeWallet(t, svc, 10_000, "THB").ID, uuid.New()
			},
			amount:  1_000,
			wantErr: domain.ErrWalletNotFound,
		},
		{
			name: "recipient is closed",
			setup: func(t *testing.T, svc service.WalletService) (uuid.UUID, uuid.UUID) {
				from := makeWallet(t, svc, 10_000, "THB")
				to := makeWallet(t, svc, 0, "THB")
				if _, err := svc.SetStatus(ctx, to.ID, domain.WalletStatusClosed); err != nil {
					t.Fatalf("close recipient: %v", err)
				}
				return from.ID, to.ID
			},
			amount:  1_000,
			wantErr: domain.ErrWalletInactive,
		},
		{
			name: "currency mismatch",
			setup: func(t *testing.T, svc service.WalletService) (uuid.UUID, uuid.UUID) {
				return makeWallet(t, svc, 10_000, "THB").ID, makeWallet(t, svc, 0, "USD").ID
			},
			amount:  1_000,
			wantErr: domain.ErrCurrencyMismatch,
		},
		{
			name: "insufficient balance",
			setup: func(t *testing.T, svc service.WalletService) (uuid.UUID, uuid.UUID) {
				return makeWallet(t, svc, 500, "THB").ID, makeWallet(t, svc, 0, "THB").ID
			},
			amount:  100_000,
			wantErr: domain.ErrInsufficientBalance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, db := newTestService(t)
			fromID, toID := tt.setup(t, svc)

			beforeFrom := balanceOf(t, svc, fromID)
			beforeCount := countTransactions(t, db, fromID)

			_, err := svc.Transfer(ctx, fromID, toID, tt.amount, "doomed", nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			// The debit is rolled back with everything else: there is no state
			// in which money left the sender without arriving.
			if got := balanceOf(t, svc, fromID); got != beforeFrom {
				t.Errorf("sender balance = %d, want %d (a partial debit survived)", got, beforeFrom)
			}
			// The ledger row inserted at the start of the transaction is rolled
			// back too, so a failed transfer leaves no trace.
			if got := countTransactions(t, db, fromID); got != beforeCount {
				t.Errorf("transaction count = %d, want %d (a failed transfer was recorded)", got, beforeCount)
			}

			assertWalletConsistent(t, db, fromID)
		})
	}
}

// --- 5. the backend dying mid-transaction ---

// Simulates the server going down between the debit and the commit. Postgres
// rolls the whole transaction back during recovery, so the "money left but
// never arrived" state is unreachable -- there is nothing for the application
// to clean up.
func TestBackendKilledMidTransaction_NoPartialState(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	from := makeWallet(t, svc, 10_000, "THB")
	to := makeWallet(t, svc, 0, "THB")

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}

	// A pinned connection: the pool must not hand this transaction's work to a
	// different physical connection halfway through.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	defer conn.Close() //nolint:errcheck // the connection is deliberately killed below

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	var pid int
	if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("read backend pid: %v", err)
	}

	// Debit the sender, then stop short of committing.
	if _, err := tx.ExecContext(ctx,
		"UPDATE wallets SET balance = balance - 5000 WHERE id = $1", from.ID); err != nil {
		t.Fatalf("debit: %v", err)
	}

	// Kill the backend from a different connection: the crash.
	if _, err := sqlDB.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatalf("terminate backend: %v", err)
	}

	// The commit cannot succeed; the transaction died with its backend.
	if err := tx.Commit(); err == nil {
		t.Error("commit succeeded after the backend was killed, which should be impossible")
	}

	// Give Postgres a moment to finish cleaning up the dead session.
	time.Sleep(200 * time.Millisecond)

	if got := balanceOf(t, svc, from.ID); got != 10_000 {
		t.Errorf("sender = %d, want 10000: an uncommitted debit survived the crash", got)
	}
	if got := balanceOf(t, svc, to.ID); got != 0 {
		t.Errorf("recipient = %d, want 0", got)
	}

	assertWalletConsistent(t, db, from.ID)
	assertWalletConsistent(t, db, to.ID)
}

// --- 6. idempotency under concurrency ---

// The strongest single result here: ten simultaneous retries of the same
// request move the money exactly once. The guarantee comes from the UNIQUE
// index, not from application code -- a "SELECT then INSERT" check would race.
func TestIdempotency_ConcurrentDuplicateKey(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	w := makeWallet(t, svc, 0, "THB")
	key := "idem-" + uuid.NewString()

	const workers = 10
	const amount = 100

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[uuid.UUID]int{}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.Deposit(ctx, w.ID, amount, "retry", &key)
			if err != nil {
				t.Errorf("deposit failed: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[result.Transaction.ID]++
		}()
	}
	wg.Wait()

	if len(ids) != 1 {
		t.Errorf("got %d distinct transaction ids, want 1: %v", len(ids), ids)
	}
	if got := balanceOf(t, svc, w.ID); got != amount {
		t.Errorf("balance = %d, want %d: the deposit was applied more than once", got, amount)
	}
	if n := countTransactions(t, db, w.ID); n != 1 {
		t.Errorf("%d ledger entries, want 1", n)
	}

	assertWalletConsistent(t, db, w.ID)
}

// --- 7. idempotency key reuse ---

func TestIdempotency_KeyReuseWithDifferentAmount(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	w := makeWallet(t, svc, 0, "THB")
	key := "idem-" + uuid.NewString()

	if _, err := svc.Deposit(ctx, w.ID, 700, "first", &key); err != nil {
		t.Fatalf("first deposit: %v", err)
	}

	_, err := svc.Deposit(ctx, w.ID, 999, "different", &key)
	if !errors.Is(err, domain.ErrIdempotencyKeyReuse) {
		t.Errorf("err = %v, want ErrIdempotencyKeyReuse", err)
	}
	if got := balanceOf(t, svc, w.ID); got != 700 {
		t.Errorf("balance = %d, want 700", got)
	}

	assertWalletConsistent(t, db, w.ID)
}

// --- 8. the database refuses a negative balance ---

// Defence in depth: the service guard is the first line, but the CHECK
// constraint means even a bug in Go cannot drive a wallet negative.
func TestDBRejectsNegativeBalance(t *testing.T) {
	svc, _, db := newTestService(t)

	w := makeWallet(t, svc, 1_000, "THB")

	err := db.Exec("UPDATE wallets SET balance = -1 WHERE id = ?", w.ID).Error
	if err == nil {
		t.Fatal("the database accepted a negative balance; the CHECK constraint is missing")
	}

	if got := balanceOf(t, svc, w.ID); got != 1_000 {
		t.Errorf("balance = %d, want 1000", got)
	}
}

// --- 9. one wallet per user per currency ---

func TestDuplicateWalletRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	userID := "test-" + uuid.NewString()

	if _, err := svc.CreateWallet(ctx, userID, "THB"); err != nil {
		t.Fatalf("first wallet: %v", err)
	}

	_, err := svc.CreateWallet(ctx, userID, "THB")
	if !errors.Is(err, domain.ErrWalletAlreadyExists) {
		t.Errorf("err = %v, want ErrWalletAlreadyExists", err)
	}

	// A different currency for the same user is allowed.
	if _, err := svc.CreateWallet(ctx, userID, "USD"); err != nil {
		t.Errorf("second currency should be allowed, got: %v", err)
	}
}

// --- 10. closing an account races safely against a transfer ---

// SetStatus takes the same row lock as a balance change, so the outcome is
// always one of two well-defined results and never "money landed in a wallet
// that was already closed".
func TestConcurrentCloseAndTransfer(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	for round := 0; round < 10; round++ {
		from := makeWallet(t, svc, 10_000, "THB")
		to := makeWallet(t, svc, 0, "THB")

		var wg sync.WaitGroup
		var transferErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			_, transferErr = svc.Transfer(ctx, from.ID, to.ID, 1_000, "racing", nil)
		}()
		go func() {
			defer wg.Done()
			if _, err := svc.SetStatus(ctx, to.ID, domain.WalletStatusClosed); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
		wg.Wait()

		switch {
		case transferErr == nil:
			// Landed before the close: the recipient must hold the money.
			if got := balanceOf(t, svc, to.ID); got != 1_000 {
				t.Errorf("round %d: transfer reported success but recipient has %d", round, got)
			}
		case errors.Is(transferErr, domain.ErrWalletInactive):
			// Lost the race: nothing moved.
			if got := balanceOf(t, svc, to.ID); got != 0 {
				t.Errorf("round %d: transfer was rejected but recipient has %d", round, got)
			}
			if got := balanceOf(t, svc, from.ID); got != 10_000 {
				t.Errorf("round %d: transfer was rejected but sender has %d", round, got)
			}
		default:
			t.Errorf("round %d: unexpected error: %v", round, transferErr)
		}

		assertWalletConsistent(t, db, from.ID)
		assertWalletConsistent(t, db, to.ID)
	}
}
