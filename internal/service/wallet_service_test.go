package service

import (
	"context"
	"errors"
	"testing"

	"wallet-service/internal/domain"

	"github.com/google/uuid"
)

func ptr(s string) *string { return &s }

// newServiceWith returns a service plus its mock, with the given wallets seeded.
func newServiceWith(t *testing.T, wallets ...*domain.Wallet) (WalletService, *mockRepo) {
	t.Helper()
	repo := newMockRepo()
	for _, w := range wallets {
		repo.addWallet(w)
	}
	return NewWalletService(repo), repo
}

func activeWallet(balance int64, currency string) *domain.Wallet {
	return &domain.Wallet{
		ID:       uuid.New(),
		UserID:   "user-" + uuid.NewString(),
		Balance:  balance,
		Currency: currency,
		Status:   domain.WalletStatusActive,
	}
}

// --- CreateWallet ---

func TestCreateWallet(t *testing.T) {
	ctx := context.Background()

	t.Run("starts empty and active", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		w, err := svc.CreateWallet(ctx, "user-001", "THB")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.Balance != 0 {
			t.Errorf("balance = %d, want 0", w.Balance)
		}
		if w.Status != domain.WalletStatusActive {
			t.Errorf("status = %q, want ACTIVE", w.Status)
		}
	})

	t.Run("empty currency defaults to THB", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		w, err := svc.CreateWallet(ctx, "user-001", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.Currency != "THB" {
			t.Errorf("currency = %q, want THB", w.Currency)
		}
	})

	t.Run("currency is normalised to upper case", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		w, err := svc.CreateWallet(ctx, "user-001", "usd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.Currency != "USD" {
			t.Errorf("currency = %q, want USD", w.Currency)
		}
	})

	t.Run("duplicate wallet error is propagated", func(t *testing.T) {
		svc, repo := newServiceWith(t)
		repo.createWalletErr = domain.ErrWalletAlreadyExists

		_, err := svc.CreateWallet(ctx, "user-001", "THB")
		if !errors.Is(err, domain.ErrWalletAlreadyExists) {
			t.Errorf("err = %v, want ErrWalletAlreadyExists", err)
		}
	})
}

// --- GetWallet ---

func TestGetWallet(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		w := activeWallet(15000, "THB")
		svc, _ := newServiceWith(t, w)

		got, err := svc.GetWallet(ctx, w.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Balance != 15000 {
			t.Errorf("balance = %d, want 15000", got.Balance)
		}
	})

	t.Run("unknown id is not found", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		_, err := svc.GetWallet(ctx, uuid.New())
		if !errors.Is(err, domain.ErrWalletNotFound) {
			t.Errorf("err = %v, want ErrWalletNotFound", err)
		}
	})
}

// --- Deposit ---

func TestDeposit(t *testing.T) {
	ctx := context.Background()

	t.Run("credits the exact amount and logs a DEPOSIT", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		result, err := svc.Deposit(ctx, w.ID, 5000, "slip-1", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := repo.balanceOf(w.ID); got != 15000 {
			t.Errorf("balance = %d, want 15000", got)
		}

		txn := result.Transaction
		if txn.Type != domain.TransactionTypeDeposit {
			t.Errorf("type = %q, want DEPOSIT", txn.Type)
		}
		if txn.Status != domain.TransactionStatusCompleted {
			t.Errorf("status = %q, want COMPLETED", txn.Status)
		}
		if txn.FromWalletID != nil {
			t.Error("from_wallet_id should be nil for a deposit")
		}
		if txn.ToWalletID == nil || *txn.ToWalletID != w.ID {
			t.Error("to_wallet_id should be the credited wallet")
		}
		if result.Replayed {
			t.Error("a fresh deposit must not be marked as replayed")
		}
	})

	t.Run("takes the row lock rather than a plain read", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		if _, err := svc.Deposit(ctx, w.ID, 5000, "", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !containsCall(repo.callOrder, "GetWalletForUpdate") {
			t.Error("deposit must read the wallet with SELECT ... FOR UPDATE")
		}
		if containsCall(repo.callOrder, "GetWallet") {
			t.Error("deposit must not use an unlocked read")
		}
	})

	t.Run("invalid amounts are rejected before the repository is touched", func(t *testing.T) {
		for _, amount := range []int64{0, -1, domain.MaxAmountSatang + 1} {
			w := activeWallet(10000, "THB")
			svc, repo := newServiceWith(t, w)

			_, err := svc.Deposit(ctx, w.ID, amount, "", nil)
			if !errors.Is(err, domain.ErrInvalidAmount) {
				t.Errorf("amount %d: err = %v, want ErrInvalidAmount", amount, err)
			}
			if len(repo.callOrder) != 0 {
				t.Errorf("amount %d: repository was called %v", amount, repo.callOrder)
			}
		}
	})

	t.Run("closed wallet is rejected", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		w.Status = domain.WalletStatusClosed
		svc, repo := newServiceWith(t, w)

		_, err := svc.Deposit(ctx, w.ID, 5000, "", nil)
		if !errors.Is(err, domain.ErrWalletInactive) {
			t.Errorf("err = %v, want ErrWalletInactive", err)
		}
		if got := repo.balanceOf(w.ID); got != 10000 {
			t.Errorf("balance changed to %d, want 10000", got)
		}
	})

	t.Run("unknown wallet is not found", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		_, err := svc.Deposit(ctx, uuid.New(), 5000, "", nil)
		if !errors.Is(err, domain.ErrWalletNotFound) {
			t.Errorf("err = %v, want ErrWalletNotFound", err)
		}
	})

	t.Run("overflow is refused", func(t *testing.T) {
		w := activeWallet(1<<62, "THB")
		w.Balance = 9_223_372_036_854_775_800
		svc, _ := newServiceWith(t, w)

		_, err := svc.Deposit(ctx, w.ID, 1000, "", nil)
		if !errors.Is(err, domain.ErrBalanceOverflow) {
			t.Errorf("err = %v, want ErrBalanceOverflow", err)
		}
	})
}

// --- Withdraw ---

func TestWithdraw(t *testing.T) {
	ctx := context.Background()

	t.Run("debits and logs a WITHDRAW", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		result, err := svc.Withdraw(ctx, w.ID, 4000, "atm", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := repo.balanceOf(w.ID); got != 6000 {
			t.Errorf("balance = %d, want 6000", got)
		}

		txn := result.Transaction
		if txn.Type != domain.TransactionTypeWithdraw {
			t.Errorf("type = %q, want WITHDRAW", txn.Type)
		}
		if txn.ToWalletID != nil {
			t.Error("to_wallet_id should be nil for a withdrawal")
		}
		if txn.FromWalletID == nil || *txn.FromWalletID != w.ID {
			t.Error("from_wallet_id should be the debited wallet")
		}
	})

	t.Run("withdrawing the whole balance succeeds and lands on zero", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		if _, err := svc.Withdraw(ctx, w.ID, 10000, "", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := repo.balanceOf(w.ID); got != 0 {
			t.Errorf("balance = %d, want 0", got)
		}
	})

	t.Run("one satang more than the balance is refused", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		_, err := svc.Withdraw(ctx, w.ID, 10001, "", nil)
		if !errors.Is(err, domain.ErrInsufficientBalance) {
			t.Errorf("err = %v, want ErrInsufficientBalance", err)
		}
		if got := repo.balanceOf(w.ID); got != 10000 {
			t.Errorf("balance changed to %d, want 10000", got)
		}
	})

	t.Run("closed wallet is rejected", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		w.Status = domain.WalletStatusClosed
		svc, _ := newServiceWith(t, w)

		_, err := svc.Withdraw(ctx, w.ID, 1000, "", nil)
		if !errors.Is(err, domain.ErrWalletInactive) {
			t.Errorf("err = %v, want ErrWalletInactive", err)
		}
	})

	t.Run("invalid amount is rejected", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, _ := newServiceWith(t, w)

		_, err := svc.Withdraw(ctx, w.ID, 0, "", nil)
		if !errors.Is(err, domain.ErrInvalidAmount) {
			t.Errorf("err = %v, want ErrInvalidAmount", err)
		}
	})
}

// --- Transfer ---

func TestTransfer(t *testing.T) {
	ctx := context.Background()

	t.Run("moves money and conserves the total", func(t *testing.T) {
		from := activeWallet(10000, "THB")
		to := activeWallet(2000, "THB")
		svc, repo := newServiceWith(t, from, to)

		result, err := svc.Transfer(ctx, from.ID, to.ID, 2500, "rent", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gotFrom, gotTo := repo.balanceOf(from.ID), repo.balanceOf(to.ID)
		if gotFrom != 7500 {
			t.Errorf("sender = %d, want 7500", gotFrom)
		}
		if gotTo != 4500 {
			t.Errorf("receiver = %d, want 4500", gotTo)
		}
		if gotFrom+gotTo != 12000 {
			t.Errorf("total = %d, want 12000 (money was created or destroyed)", gotFrom+gotTo)
		}
		if result.Transaction.Type != domain.TransactionTypeTransfer {
			t.Errorf("type = %q, want TRANSFER", result.Transaction.Type)
		}
	})

	// The deadlock fix, asserted without a database: whichever direction the
	// transfer goes, the locks must be taken in the same order.
	t.Run("locks are acquired in the same order in both directions", func(t *testing.T) {
		a := activeWallet(10000, "THB")
		b := activeWallet(10000, "THB")

		svcAB, repoAB := newServiceWith(t, a, b)
		if _, err := svcAB.Transfer(ctx, a.ID, b.ID, 100, "", nil); err != nil {
			t.Fatalf("A->B failed: %v", err)
		}

		svcBA, repoBA := newServiceWith(t, a, b)
		if _, err := svcBA.Transfer(ctx, b.ID, a.ID, 100, "", nil); err != nil {
			t.Fatalf("B->A failed: %v", err)
		}

		if len(repoAB.lockOrder) != 2 || len(repoBA.lockOrder) != 2 {
			t.Fatalf("expected two locks each, got %v and %v", repoAB.lockOrder, repoBA.lockOrder)
		}
		if repoAB.lockOrder[0] != repoBA.lockOrder[0] || repoAB.lockOrder[1] != repoBA.lockOrder[1] {
			t.Errorf("lock order differs by direction: A->B %v, B->A %v -- this deadlocks under concurrency",
				repoAB.lockOrder, repoBA.lockOrder)
		}
	})

	t.Run("transferring to yourself is refused without touching the repository", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		_, err := svc.Transfer(ctx, w.ID, w.ID, 100, "", nil)
		if !errors.Is(err, domain.ErrSameWallet) {
			t.Errorf("err = %v, want ErrSameWallet", err)
		}
		if len(repo.callOrder) != 0 {
			t.Errorf("repository was called: %v", repo.callOrder)
		}
	})

	t.Run("insufficient balance leaves both wallets untouched", func(t *testing.T) {
		from := activeWallet(1000, "THB")
		to := activeWallet(2000, "THB")
		svc, repo := newServiceWith(t, from, to)

		_, err := svc.Transfer(ctx, from.ID, to.ID, 5000, "", nil)
		if !errors.Is(err, domain.ErrInsufficientBalance) {
			t.Fatalf("err = %v, want ErrInsufficientBalance", err)
		}
		if repo.balanceOf(from.ID) != 1000 || repo.balanceOf(to.ID) != 2000 {
			t.Errorf("balances changed: sender %d, receiver %d", repo.balanceOf(from.ID), repo.balanceOf(to.ID))
		}
	})

	t.Run("currency mismatch is refused", func(t *testing.T) {
		from := activeWallet(10000, "THB")
		to := activeWallet(0, "USD")
		svc, _ := newServiceWith(t, from, to)

		_, err := svc.Transfer(ctx, from.ID, to.ID, 100, "", nil)
		if !errors.Is(err, domain.ErrCurrencyMismatch) {
			t.Errorf("err = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("either side being closed is refused", func(t *testing.T) {
		cases := []struct {
			name           string
			closeSender    bool
			closeRecipient bool
		}{
			{"sender closed", true, false},
			{"recipient closed", false, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				from := activeWallet(10000, "THB")
				to := activeWallet(0, "THB")
				if tc.closeSender {
					from.Status = domain.WalletStatusClosed
				}
				if tc.closeRecipient {
					to.Status = domain.WalletStatusClosed
				}
				svc, repo := newServiceWith(t, from, to)

				_, err := svc.Transfer(ctx, from.ID, to.ID, 100, "", nil)
				if !errors.Is(err, domain.ErrWalletInactive) {
					t.Errorf("err = %v, want ErrWalletInactive", err)
				}
				if repo.balanceOf(from.ID) != 10000 || repo.balanceOf(to.ID) != 0 {
					t.Error("balances must not change when a wallet is closed")
				}
			})
		}
	})

	t.Run("missing recipient leaves the sender untouched", func(t *testing.T) {
		from := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, from)

		_, err := svc.Transfer(ctx, from.ID, uuid.New(), 100, "", nil)
		if !errors.Is(err, domain.ErrWalletNotFound) {
			t.Fatalf("err = %v, want ErrWalletNotFound", err)
		}
		if repo.balanceOf(from.ID) != 10000 {
			t.Errorf("sender = %d, want 10000", repo.balanceOf(from.ID))
		}
	})
}

// --- Idempotency ---

func TestIdempotency(t *testing.T) {
	ctx := context.Background()

	t.Run("replaying a key returns the original without moving money again", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)
		key := ptr("key-abc")

		first, err := svc.Deposit(ctx, w.ID, 700, "", key)
		if err != nil {
			t.Fatalf("first deposit: %v", err)
		}
		if first.Replayed {
			t.Error("first call must not be a replay")
		}

		second, err := svc.Deposit(ctx, w.ID, 700, "", key)
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		if !second.Replayed {
			t.Error("retry must be marked as replayed")
		}
		if second.Transaction.ID != first.Transaction.ID {
			t.Error("retry must return the original transaction")
		}
		if got := repo.balanceOf(w.ID); got != 10700 {
			t.Errorf("balance = %d, want 10700 (the deposit was applied twice)", got)
		}
	})

	t.Run("the ledger entry is written before any wallet is locked", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		if _, err := svc.Deposit(ctx, w.ID, 700, "", ptr("key-order")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		createAt := indexOfCall(repo.callOrder, "CreateTransaction")
		lockAt := indexOfCall(repo.callOrder, "GetWalletForUpdate")
		if createAt == -1 || lockAt == -1 {
			t.Fatalf("expected both calls, got %v", repo.callOrder)
		}
		if createAt > lockAt {
			t.Errorf("CreateTransaction must run before GetWalletForUpdate so a duplicate key aborts before taking locks; got %v", repo.callOrder)
		}
	})

	t.Run("reusing a key for a different amount is refused", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)
		key := ptr("key-reuse")

		if _, err := svc.Deposit(ctx, w.ID, 700, "", key); err != nil {
			t.Fatalf("first deposit: %v", err)
		}

		_, err := svc.Deposit(ctx, w.ID, 999, "", key)
		if !errors.Is(err, domain.ErrIdempotencyKeyReuse) {
			t.Errorf("err = %v, want ErrIdempotencyKeyReuse", err)
		}
		if got := repo.balanceOf(w.ID); got != 10700 {
			t.Errorf("balance = %d, want 10700", got)
		}
	})

	t.Run("without a key every request is applied", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, repo := newServiceWith(t, w)

		for i := 0; i < 3; i++ {
			if _, err := svc.Deposit(ctx, w.ID, 100, "", nil); err != nil {
				t.Fatalf("deposit %d: %v", i, err)
			}
		}
		if got := repo.balanceOf(w.ID); got != 10300 {
			t.Errorf("balance = %d, want 10300", got)
		}
	})
}

// --- ListTransactions ---

func TestListTransactions(t *testing.T) {
	ctx := context.Background()

	t.Run("limit and offset are normalised", func(t *testing.T) {
		tests := []struct {
			name                  string
			limit, offset         int
			wantLimit, wantOffset int
		}{
			{"zero limit falls back to the default", 0, 0, 20, 0},
			{"negative limit falls back to the default", -5, 0, 20, 0},
			{"oversized limit is capped", 1000, 0, 100, 0},
			{"negative offset is clamped", 20, -1, 20, 0},
			{"valid values pass through", 50, 10, 50, 10},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				w := activeWallet(0, "THB")
				svc, _ := newServiceWith(t, w)

				page, err := svc.ListTransactions(ctx, w.ID, tt.limit, tt.offset)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if page.Limit != tt.wantLimit {
					t.Errorf("limit = %d, want %d", page.Limit, tt.wantLimit)
				}
				if page.Offset != tt.wantOffset {
					t.Errorf("offset = %d, want %d", page.Offset, tt.wantOffset)
				}
			})
		}
	})

	t.Run("unknown wallet is not found rather than an empty page", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		_, err := svc.ListTransactions(ctx, uuid.New(), 20, 0)
		if !errors.Is(err, domain.ErrWalletNotFound) {
			t.Errorf("err = %v, want ErrWalletNotFound", err)
		}
	})

	t.Run("no history yields an empty, non-nil slice", func(t *testing.T) {
		w := activeWallet(0, "THB")
		svc, _ := newServiceWith(t, w)

		page, err := svc.ListTransactions(ctx, w.ID, 20, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if page.Items == nil {
			t.Error("items must be non-nil so the JSON is [] and not null")
		}
		if page.Total != 0 {
			t.Errorf("total = %d, want 0", page.Total)
		}
	})

	t.Run("counts both incoming and outgoing entries", func(t *testing.T) {
		a := activeWallet(10000, "THB")
		b := activeWallet(0, "THB")
		svc, _ := newServiceWith(t, a, b)

		if _, err := svc.Deposit(ctx, a.ID, 1000, "", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Transfer(ctx, a.ID, b.ID, 500, "", nil); err != nil {
			t.Fatal(err)
		}

		page, err := svc.ListTransactions(ctx, b.ID, 20, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if page.Total != 1 {
			t.Errorf("recipient total = %d, want 1", page.Total)
		}
	})
}

// --- SetStatus ---

func TestSetStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("closes and reopens", func(t *testing.T) {
		w := activeWallet(10000, "THB")
		svc, _ := newServiceWith(t, w)

		closed, err := svc.SetStatus(ctx, w.ID, domain.WalletStatusClosed)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if closed.Status != domain.WalletStatusClosed {
			t.Errorf("status = %q, want CLOSED", closed.Status)
		}
		// Closing preserves the balance; the funds are frozen, not lost.
		if closed.Balance != 10000 {
			t.Errorf("balance = %d, want 10000", closed.Balance)
		}

		reopened, err := svc.SetStatus(ctx, w.ID, domain.WalletStatusActive)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if reopened.Status != domain.WalletStatusActive {
			t.Errorf("status = %q, want ACTIVE", reopened.Status)
		}
	})

	// Changing the flag has to be serialised against the operations that read
	// it, so it takes the same row lock as a balance change.
	t.Run("takes the row lock", func(t *testing.T) {
		w := activeWallet(0, "THB")
		svc, repo := newServiceWith(t, w)

		if _, err := svc.SetStatus(ctx, w.ID, domain.WalletStatusClosed); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !containsCall(repo.callOrder, "GetWalletForUpdate") {
			t.Error("SetStatus must lock the row: the status is a precondition of transfers")
		}
	})

	t.Run("unknown status is rejected", func(t *testing.T) {
		w := activeWallet(0, "THB")
		svc, repo := newServiceWith(t, w)

		_, err := svc.SetStatus(ctx, w.ID, domain.WalletStatus("FROZEN"))
		if !errors.Is(err, domain.ErrInvalidStatus) {
			t.Errorf("err = %v, want ErrInvalidStatus", err)
		}
		if len(repo.callOrder) != 0 {
			t.Errorf("repository was called: %v", repo.callOrder)
		}
	})

	t.Run("unknown wallet is not found", func(t *testing.T) {
		svc, _ := newServiceWith(t)
		_, err := svc.SetStatus(ctx, uuid.New(), domain.WalletStatusClosed)
		if !errors.Is(err, domain.ErrWalletNotFound) {
			t.Errorf("err = %v, want ErrWalletNotFound", err)
		}
	})
}

// --- helpers ---

func containsCall(calls []string, name string) bool {
	return indexOfCall(calls, name) != -1
}

func indexOfCall(calls []string, name string) int {
	for i, c := range calls {
		if c == name {
			return i
		}
	}
	return -1
}
