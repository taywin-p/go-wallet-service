package domain

import (
	"fmt"
	"math"
)

// Money is stored as int64 minor units (satang): 100 == 1.00 THB.
//
// float64 cannot represent 0.1 exactly, so 0.1 + 0.2 != 0.3. A decimal(20,2)
// column in Postgres is precise, but GORM scans it into a float64 in Go, which
// means the corruption happens in the application even when the database is
// correct. Integers make the arithmetic exact by construction.
const (
	// SatangPerUnit is the number of minor units in one major unit.
	SatangPerUnit = 100

	// MaxAmountSatang caps a single operation at 10 billion THB. Without a cap,
	// repeated deposits of astronomically large amounts could overflow int64.
	MaxAmountSatang int64 = 1_000_000_000_000
)

// FormatSatang renders minor units as a decimal string: 15000 -> "150.00".
func FormatSatang(v int64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/SatangPerUnit, v%SatangPerUnit)
}

// ValidateAmount rejects non-positive amounts and amounts beyond the per-operation cap.
func ValidateAmount(amount int64) error {
	if amount <= 0 || amount > MaxAmountSatang {
		return ErrInvalidAmount
	}
	return nil
}

// AddBalance adds amount to balance, refusing to wrap around int64.
func AddBalance(balance, amount int64) (int64, error) {
	if balance > math.MaxInt64-amount {
		return 0, ErrBalanceOverflow
	}
	return balance + amount, nil
}
