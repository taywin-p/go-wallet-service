package domain

import (
	"time"

	"github.com/google/uuid"
)

// WalletStatus is the account lifecycle state. A string enum rather than a
// bool: "CLOSED" is self-describing in JSON and in psql, it matches the
// existing TransactionStatus/TransactionType pattern, and adding a third state
// later is a new constant instead of a schema change.
type WalletStatus string

const (
	WalletStatusActive WalletStatus = "ACTIVE"
	WalletStatusClosed WalletStatus = "CLOSED"
)

func (s WalletStatus) Valid() bool {
	return s == WalletStatusActive || s == WalletStatusClosed
}

type Wallet struct {
	ID uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`

	// One wallet per user per currency. A business rule, not a technical limit
	// -- drop the composite index to allow several wallets in one currency.
	UserID   string `gorm:"type:varchar(100);not null;uniqueIndex:idx_wallet_user_currency" json:"user_id"`
	Currency string `gorm:"type:varchar(3);not null;default:'THB';uniqueIndex:idx_wallet_user_currency" json:"currency"`

	// Minor units (satang). The CHECK constraint is defence in depth: even if
	// the service guard were bypassed, the database refuses a negative balance.
	Balance int64 `gorm:"type:bigint;not null;default:0;check:chk_wallet_balance_non_negative,balance >= 0" json:"balance"`

	Status WalletStatus `gorm:"type:varchar(20);not null;default:'ACTIVE'" json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (w *Wallet) IsActive() bool {
	return w.Status == WalletStatusActive
}
