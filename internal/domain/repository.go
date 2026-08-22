package domain

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the persistence port. It lives in the domain package so the
// dependency arrow points inward: handler -> service -> domain <- repository.
// The service depends on this interface and never imports GORM.
//
// Implemented by internal/repository (GORM/Postgres) and by a hand-written
// mock in the service tests.
type Repository interface {
	// WithTx runs fn inside a single database transaction. The Repository
	// handed to fn is scoped to that transaction; the transaction is rolled
	// back if fn returns an error or panics, and committed otherwise. A failed
	// commit surfaces as the returned error.
	//
	// Making the transaction a closure rather than an explicit Begin/Commit
	// pair is what removes three whole bug classes: an unchecked commit, a
	// missing rollback on panic, and a mutation accidentally left outside the
	// transaction.
	WithTx(ctx context.Context, fn func(r Repository) error) error

	CreateWallet(ctx context.Context, w *Wallet) error
	GetWallet(ctx context.Context, id uuid.UUID) (*Wallet, error)
	// GetWalletForUpdate issues SELECT ... FOR UPDATE. Only meaningful inside WithTx.
	GetWalletForUpdate(ctx context.Context, id uuid.UUID) (*Wallet, error)
	UpdateWallet(ctx context.Context, w *Wallet) error

	CreateTransaction(ctx context.Context, t *Transaction) error
	ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]Transaction, error)
	CountTransactions(ctx context.Context, walletID uuid.UUID) (int64, error)

	// GetTransactionByIdempotencyKey is used on retry. It must be called
	// outside WithTx: after a unique-index violation the transaction is
	// aborted and cannot serve further queries.
	GetTransactionByIdempotencyKey(ctx context.Context, key string) (*Transaction, error)
}
