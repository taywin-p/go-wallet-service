package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"wallet-service/internal/domain"

	"github.com/gofiber/fiber/v2"
)

func TestRespondError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"wallet not found", domain.ErrWalletNotFound, 404, "WALLET_NOT_FOUND"},
		{"transaction not found", domain.ErrTransactionNotFound, 404, "TRANSACTION_NOT_FOUND"},
		{"wallet inactive", domain.ErrWalletInactive, 409, "WALLET_INACTIVE"},
		{"wallet already exists", domain.ErrWalletAlreadyExists, 409, "WALLET_ALREADY_EXISTS"},
		{"currency mismatch", domain.ErrCurrencyMismatch, 409, "CURRENCY_MISMATCH"},
		{"insufficient balance", domain.ErrInsufficientBalance, 422, "INSUFFICIENT_BALANCE"},
		{"balance overflow", domain.ErrBalanceOverflow, 422, "BALANCE_OVERFLOW"},
		{"idempotency key reuse", domain.ErrIdempotencyKeyReuse, 422, "IDEMPOTENCY_KEY_REUSE"},
		{"invalid amount", domain.ErrInvalidAmount, 400, "INVALID_AMOUNT"},
		{"same wallet", domain.ErrSameWallet, 400, "SAME_WALLET"},
		{"invalid status", domain.ErrInvalidStatus, 400, "INVALID_STATUS"},

		// The repository wraps errors with %w, so matching must survive wrapping.
		{"wrapped once", fmt.Errorf("get wallet: %w", domain.ErrWalletNotFound), 404, "WALLET_NOT_FOUND"},
		{"wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", domain.ErrInsufficientBalance)), 422, "INSUFFICIENT_BALANCE"},

		// Anything unrecognised must not leak its detail to the client.
		{"unknown error", errors.New("connection refused to 10.0.0.5:5432"), 500, "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error { return respondError(c, tt.err) })

			resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			var body apiError
			raw, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("response is not JSON: %s", raw)
			}
			if body.Error != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error, tt.wantCode)
			}
		})
	}
}

func TestRespondErrorHidesInternalDetail(t *testing.T) {
	app := fiber.New()
	secret := "password=hunter2 host=internal-db.prod"
	app.Get("/", func(c *fiber.Ctx) error { return respondError(c, errors.New(secret)) })

	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if string(raw) == "" {
		t.Fatal("empty response")
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Errorf("internal detail leaked to the client: %s", raw)
	}
}
