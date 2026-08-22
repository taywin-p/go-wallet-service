package domain

import (
	"time"

	"github.com/google/uuid"
)

type TransactionType string
type TransactionStatus string

const (
	TransactionTypeDeposit  TransactionType = "DEPOSIT"
	TransactionTypeWithdraw TransactionType = "WITHDRAW"
	TransactionTypeTransfer TransactionType = "TRANSFER"

	// Every transaction here is written as COMPLETED. Inside a single database
	// transaction, writing PENDING first and updating it afterwards adds no
	// information -- both rows commit together. PENDING starts to mean
	// something only when money moves through a system that cannot commit
	// atomically with our database (an external payment gateway), which also
	// requires an outbox and a reconciliation job.
	TransactionStatusPending   TransactionStatus = "PENDING"
	TransactionStatusCompleted TransactionStatus = "COMPLETED"
	TransactionStatusFailed    TransactionStatus = "FAILED"
)

// Transaction is an append-only ledger entry: rows are never updated or
// deleted. A mistaken transfer is corrected with a reversing entry, not by
// editing history.
type Transaction struct {
	ID           uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	FromWalletID *uuid.UUID `gorm:"type:uuid;index" json:"from_wallet_id,omitempty"` // nil for DEPOSIT
	ToWalletID   *uuid.UUID `gorm:"type:uuid;index" json:"to_wallet_id,omitempty"`   // nil for WITHDRAW

	// Minor units (satang), same convention as Wallet.Balance.
	Amount int64 `gorm:"type:bigint;not null" json:"amount"`

	Type      TransactionType   `gorm:"type:varchar(20);not null" json:"type"`
	Status    TransactionStatus `gorm:"type:varchar(20);not null;default:'PENDING'" json:"status"`
	Reference string            `gorm:"type:varchar(255)" json:"reference,omitempty"`

	// IdempotencyKey makes a retried request safe. It is a pointer because
	// Postgres does not treat NULLs as equal in a unique index, so any number
	// of requests may omit the key while any single key can only ever be
	// inserted once. That UNIQUE index -- not application code -- is what
	// actually guarantees the money moves once.
	IdempotencyKey *string `gorm:"type:varchar(255);uniqueIndex:idx_transaction_idempotency_key" json:"idempotency_key,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// SameOperationAs reports whether other describes the identical operation.
// Used to reject a client that reuses one idempotency key for two different
// requests, which would otherwise silently return the wrong record.
func (t *Transaction) SameOperationAs(other *Transaction) bool {
	return t.Amount == other.Amount &&
		t.Type == other.Type &&
		equalWalletID(t.FromWalletID, other.FromWalletID) &&
		equalWalletID(t.ToWalletID, other.ToWalletID)
}

func equalWalletID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
