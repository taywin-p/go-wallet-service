package service

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"wallet-service/internal/domain"

	"github.com/google/uuid"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

type TransactionPage struct {
	Items  []domain.Transaction
	Limit  int
	Offset int
	Total  int64
}

// TransactionResult carries the transaction plus whether it was created now or
// replayed from a previous request with the same idempotency key. The handler
// turns that into 201 vs 200.
type TransactionResult struct {
	Transaction *domain.Transaction
	Replayed    bool
}

type WalletService interface {
	CreateWallet(ctx context.Context, userID, currency string) (*domain.Wallet, error)
	GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error)
	Deposit(ctx context.Context, walletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error)
	Withdraw(ctx context.Context, walletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error)
	Transfer(ctx context.Context, fromWalletID, toWalletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error)
	ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) (*TransactionPage, error)
	SetStatus(ctx context.Context, walletID uuid.UUID, status domain.WalletStatus) (*domain.Wallet, error)
}

type walletService struct {
	repo domain.Repository
}

func NewWalletService(repo domain.Repository) WalletService {
	return &walletService{repo: repo}
}

func (s *walletService) CreateWallet(ctx context.Context, userID, currency string) (*domain.Wallet, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "THB"
	}

	wallet := &domain.Wallet{
		UserID:   userID,
		Currency: currency,
		Balance:  0,
		Status:   domain.WalletStatusActive,
	}

	if err := s.repo.CreateWallet(ctx, wallet); err != nil {
		return nil, err
	}
	return wallet, nil
}

func (s *walletService) GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error) {
	return s.repo.GetWallet(ctx, walletID)
}

func (s *walletService) Deposit(ctx context.Context, walletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error) {
	if err := domain.ValidateAmount(amount); err != nil {
		return nil, err
	}

	txn := &domain.Transaction{
		ToWalletID:     &walletID,
		Amount:         amount,
		Type:           domain.TransactionTypeDeposit,
		Status:         domain.TransactionStatusCompleted,
		Reference:      reference,
		IdempotencyKey: idempotencyKey,
	}

	err := s.repo.WithTx(ctx, func(r domain.Repository) error {
		// Insert the ledger entry first: a duplicate idempotency key trips the
		// unique index here and aborts before any wallet row is locked.
		if err := r.CreateTransaction(ctx, txn); err != nil {
			return err
		}

		wallet, err := r.GetWalletForUpdate(ctx, walletID)
		if err != nil {
			return err
		}
		if !wallet.IsActive() {
			return domain.ErrWalletInactive
		}

		newBalance, err := domain.AddBalance(wallet.Balance, amount)
		if err != nil {
			return err
		}
		wallet.Balance = newBalance
		return r.UpdateWallet(ctx, wallet)
	})

	return s.finish(ctx, txn, err, idempotencyKey)
}

func (s *walletService) Withdraw(ctx context.Context, walletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error) {
	if err := domain.ValidateAmount(amount); err != nil {
		return nil, err
	}

	txn := &domain.Transaction{
		FromWalletID:   &walletID,
		Amount:         amount,
		Type:           domain.TransactionTypeWithdraw,
		Status:         domain.TransactionStatusCompleted,
		Reference:      reference,
		IdempotencyKey: idempotencyKey,
	}

	err := s.repo.WithTx(ctx, func(r domain.Repository) error {
		if err := r.CreateTransaction(ctx, txn); err != nil {
			return err
		}

		wallet, err := r.GetWalletForUpdate(ctx, walletID)
		if err != nil {
			return err
		}
		if !wallet.IsActive() {
			return domain.ErrWalletInactive
		}
		if wallet.Balance < amount {
			return domain.ErrInsufficientBalance
		}

		wallet.Balance -= amount
		return r.UpdateWallet(ctx, wallet)
	})

	return s.finish(ctx, txn, err, idempotencyKey)
}

func (s *walletService) Transfer(ctx context.Context, fromWalletID, toWalletID uuid.UUID, amount int64, reference string, idempotencyKey *string) (*TransactionResult, error) {
	if err := domain.ValidateAmount(amount); err != nil {
		return nil, err
	}
	if fromWalletID == toWalletID {
		return nil, domain.ErrSameWallet
	}

	txn := &domain.Transaction{
		FromWalletID:   &fromWalletID,
		ToWalletID:     &toWalletID,
		Amount:         amount,
		Type:           domain.TransactionTypeTransfer,
		Status:         domain.TransactionStatusCompleted,
		Reference:      reference,
		IdempotencyKey: idempotencyKey,
	}

	err := s.repo.WithTx(ctx, func(r domain.Repository) error {
		if err := r.CreateTransaction(ctx, txn); err != nil {
			return err
		}

		sender, receiver, err := lockPair(ctx, r, fromWalletID, toWalletID)
		if err != nil {
			return err
		}

		if !sender.IsActive() || !receiver.IsActive() {
			return domain.ErrWalletInactive
		}
		if sender.Currency != receiver.Currency {
			return domain.ErrCurrencyMismatch
		}
		if sender.Balance < amount {
			return domain.ErrInsufficientBalance
		}

		newReceiverBalance, err := domain.AddBalance(receiver.Balance, amount)
		if err != nil {
			return err
		}

		sender.Balance -= amount
		receiver.Balance = newReceiverBalance

		if err := r.UpdateWallet(ctx, sender); err != nil {
			return err
		}
		return r.UpdateWallet(ctx, receiver)
	})

	return s.finish(ctx, txn, err, idempotencyKey)
}

// lockPair acquires both wallet row locks in a globally consistent order,
// sorted by wallet UUID. Locking sender-then-receiver instead would deadlock:
// a transfer A->B and a concurrent B->A each hold the lock the other needs,
// and Postgres kills one of them with SQLSTATE 40P01.
func lockPair(ctx context.Context, r domain.Repository, fromID, toID uuid.UUID) (sender, receiver *domain.Wallet, err error) {
	first, second := fromID, toID
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}

	walletFirst, err := r.GetWalletForUpdate(ctx, first)
	if err != nil {
		return nil, nil, err
	}
	walletSecond, err := r.GetWalletForUpdate(ctx, second)
	if err != nil {
		return nil, nil, err
	}

	if first == fromID {
		return walletFirst, walletSecond, nil
	}
	return walletSecond, walletFirst, nil
}

func (s *walletService) ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) (*TransactionPage, error) {
	// Resolve the wallet first so a mistyped UUID is a 404 rather than a
	// misleading empty 200.
	if _, err := s.repo.GetWallet(ctx, walletID); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	if offset < 0 {
		offset = 0
	}

	items, err := s.repo.ListTransactions(ctx, walletID, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountTransactions(ctx, walletID)
	if err != nil {
		return nil, err
	}

	return &TransactionPage{Items: items, Limit: limit, Offset: offset, Total: total}, nil
}

func (s *walletService) SetStatus(ctx context.Context, walletID uuid.UUID, status domain.WalletStatus) (*domain.Wallet, error) {
	if !status.Valid() {
		return nil, domain.ErrInvalidStatus
	}

	var updated *domain.Wallet
	err := s.repo.WithTx(ctx, func(r domain.Repository) error {
		// Locking matters even though this only flips a flag: the flag is a
		// precondition of deposit/withdraw/transfer, so closing an account
		// concurrently with an in-flight transfer must be serialised against it.
		wallet, err := r.GetWalletForUpdate(ctx, walletID)
		if err != nil {
			return err
		}
		wallet.Status = status
		if err := r.UpdateWallet(ctx, wallet); err != nil {
			return err
		}
		updated = wallet
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// finish interprets the outcome of a money-moving transaction. A duplicate
// idempotency key means the operation already ran: the transaction was rolled
// back, so the original record is read back outside it and replayed to the
// caller instead of moving money a second time.
func (s *walletService) finish(ctx context.Context, attempted *domain.Transaction, err error, idempotencyKey *string) (*TransactionResult, error) {
	if err == nil {
		return &TransactionResult{Transaction: attempted}, nil
	}
	if !errors.Is(err, domain.ErrDuplicateIdempotencyKey) || idempotencyKey == nil {
		return nil, err
	}

	previous, getErr := s.repo.GetTransactionByIdempotencyKey(ctx, *idempotencyKey)
	if getErr != nil {
		return nil, getErr
	}
	if !previous.SameOperationAs(attempted) {
		return nil, domain.ErrIdempotencyKeyReuse
	}
	return &TransactionResult{Transaction: previous, Replayed: true}, nil
}
