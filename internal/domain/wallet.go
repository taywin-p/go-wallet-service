package domain

import (
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID    string    `gorm:"type:varchar(100);not null" json:"user_id"`
	Balance   float64   `gorm:"type:decimal(20,2);default:0.00;not null" json:"balance"`
	Currency  string    `gorm:"type:varchar(3);not null;default:'THB'" json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
