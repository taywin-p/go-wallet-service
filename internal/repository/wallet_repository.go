package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"wallet-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// gormRepository is the Postgres adapter for domain.Repository. Its only field
// is a *gorm.DB, which is either the pooled handle or a transaction handle --
// WithTx returns a copy carrying the latter, so every method works unchanged
// inside and outside a transaction.
type gormRepository struct {
	db *gorm.DB
}

func NewWalletRepository(db *gorm.DB) domain.Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) WithTx(ctx context.Context, fn func(domain.Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormRepository{db: tx})
	})
}

func (r *gormRepository) CreateWallet(ctx context.Context, w *domain.Wallet) error {
	err := r.db.WithContext(ctx).Create(w).Error
	if isUniqueViolation(err, "idx_wallet_user_currency") {
		return domain.ErrWalletAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("create wallet: %w", err)
	}
	return nil
}

func (r *gormRepository) GetWallet(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	var w domain.Wallet
	err := r.db.WithContext(ctx).First(&w, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Translated here so the service and handler get a 404 without either
		// of them ever importing GORM.
		return nil, domain.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return &w, nil
}

func (r *gormRepository) GetWalletForUpdate(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	var w domain.Wallet
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&w, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet for update: %w", err)
	}
	return &w, nil
}

func (r *gormRepository) UpdateWallet(ctx context.Context, w *domain.Wallet) error {
	err := r.db.WithContext(ctx).Save(w).Error
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}
	return nil
}

func (r *gormRepository) CreateTransaction(ctx context.Context, t *domain.Transaction) error {
	err := r.db.WithContext(ctx).Create(t).Error
	if isUniqueViolation(err, "idempotency_key") {
		return domain.ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return fmt.Errorf("create transaction: %w", err)
	}
	return nil
}

func (r *gormRepository) ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]domain.Transaction, error) {
	out := []domain.Transaction{}
	err := r.db.WithContext(ctx).
		Where("from_wallet_id = ? OR to_wallet_id = ?", walletID, walletID).
		// The id tiebreaker matters: several transactions in a burst can share
		// a created_at, and without it pagination can repeat or skip rows.
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	return out, nil
}

func (r *gormRepository) CountTransactions(ctx context.Context, walletID uuid.UUID) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).
		Model(&domain.Transaction{}).
		Where("from_wallet_id = ? OR to_wallet_id = ?", walletID, walletID).
		Count(&total).Error
	if err != nil {
		return 0, fmt.Errorf("count transactions: %w", err)
	}
	return total, nil
}

func (r *gormRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error) {
	var t domain.Transaction
	err := r.db.WithContext(ctx).First(&t, "idempotency_key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get transaction by idempotency key: %w", err)
	}
	return &t, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) naming the given index. Matching on the index
// keeps two different unique constraints from being confused for each other.
func isUniqueViolation(err error, indexName string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, indexName)
}
