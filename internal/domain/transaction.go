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

	TransactionStatusPending   TransactionStatus = "PENDING"
	TransactionStatusCompleted TransactionStatus = "COMPLETED"
	TransactionStatusFailed    TransactionStatus = "FAILED"
)

type Transaction struct {
	ID           uuid.UUID         `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	FromWalletID *uuid.UUID        `gorm:"type:uuid;index" json:"from_wallet_id,omitempty"` // Nullable for DEPOSIT
	ToWalletID   *uuid.UUID        `gorm:"type:uuid;index" json:"to_wallet_id,omitempty"`   // Nullable for WITHDRAW
	Amount       float64           `gorm:"type:decimal(20,2);not null" json:"amount"`
	Type         TransactionType   `gorm:"type:varchar(20);not null" json:"type"`
	Status       TransactionStatus `gorm:"type:varchar(20);not null;default:'PENDING'" json:"status"`
	Reference    string            `gorm:"type:varchar(255)" json:"reference,omitempty"` // For external ref
	CreatedAt    time.Time         `json:"created_at"`
}
