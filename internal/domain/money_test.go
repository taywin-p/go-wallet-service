package domain

import (
	"errors"
	"math"
	"testing"
)

func TestFormatSatang(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero", 0, "0.00"},
		{"single satang", 5, "0.05"},
		{"under one baht", 99, "0.99"},
		{"exactly one baht", 100, "1.00"},
		{"typical amount", 15000, "150.00"},
		{"trailing satang", 12345, "123.45"},
		{"negative", -2550, "-25.50"},
		{"large", 1_000_000_000_000, "10000000000.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatSatang(tt.input); got != tt.want {
				t.Errorf("FormatSatang(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		wantErr error
	}{
		{"positive", 15000, nil},
		{"one satang", 1, nil},
		{"at the cap", MaxAmountSatang, nil},
		{"zero", 0, ErrInvalidAmount},
		{"negative", -1, ErrInvalidAmount},
		{"above the cap", MaxAmountSatang + 1, ErrInvalidAmount},
		{"max int64", math.MaxInt64, ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAmount(tt.amount)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateAmount(%d) = %v, want %v", tt.amount, err, tt.wantErr)
			}
		})
	}
}

func TestAddBalance(t *testing.T) {
	t.Run("normal addition", func(t *testing.T) {
		got, err := AddBalance(10000, 5000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 15000 {
			t.Errorf("AddBalance(10000, 5000) = %d, want 15000", got)
		}
	})

	t.Run("overflow is refused, not wrapped", func(t *testing.T) {
		_, err := AddBalance(math.MaxInt64, 1)
		if !errors.Is(err, ErrBalanceOverflow) {
			t.Errorf("AddBalance(MaxInt64, 1) = %v, want ErrBalanceOverflow", err)
		}
	})

	t.Run("exactly at the boundary still fits", func(t *testing.T) {
		got, err := AddBalance(math.MaxInt64-1, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != math.MaxInt64 {
			t.Errorf("got %d, want %d", got, int64(math.MaxInt64))
		}
	})
}
