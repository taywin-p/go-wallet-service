package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wallet-service/internal/domain"
	"wallet-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// mockService is a hand-written stand-in for service.WalletService. These
// tests exercise the HTTP layer -- routing, decoding, validation, status codes
// and the JSON contract -- with no database involved.
type mockService struct {
	wallet *domain.Wallet
	result *service.TransactionResult
	page   *service.TransactionPage
	err    error

	// Captured so tests can assert what the handler forwarded.
	gotIdempotencyKey *string
	gotAmount         int64
	gotLimit          int
	gotOffset         int
	gotStatus         domain.WalletStatus
}

func (m *mockService) CreateWallet(ctx context.Context, userID, currency string) (*domain.Wallet, error) {
	return m.wallet, m.err
}

func (m *mockService) GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error) {
	return m.wallet, m.err
}

func (m *mockService) Deposit(ctx context.Context, walletID uuid.UUID, amount int64, reference string, key *string) (*service.TransactionResult, error) {
	m.gotIdempotencyKey, m.gotAmount = key, amount
	return m.result, m.err
}

func (m *mockService) Withdraw(ctx context.Context, walletID uuid.UUID, amount int64, reference string, key *string) (*service.TransactionResult, error) {
	m.gotIdempotencyKey, m.gotAmount = key, amount
	return m.result, m.err
}

func (m *mockService) Transfer(ctx context.Context, from, to uuid.UUID, amount int64, reference string, key *string) (*service.TransactionResult, error) {
	m.gotIdempotencyKey, m.gotAmount = key, amount
	return m.result, m.err
}

func (m *mockService) ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) (*service.TransactionPage, error) {
	m.gotLimit, m.gotOffset = limit, offset
	return m.page, m.err
}

func (m *mockService) SetStatus(ctx context.Context, walletID uuid.UUID, status domain.WalletStatus) (*domain.Wallet, error) {
	m.gotStatus = status
	return m.wallet, m.err
}

var _ service.WalletService = (*mockService)(nil)

// --- helpers ---

func newTestApp(svc service.WalletService) *fiber.App {
	h := NewWalletHandler(svc)
	app := fiber.New()
	v1 := app.Group("/api/v1")
	v1.Post("/wallets", h.CreateWallet)
	v1.Get("/wallets/:id", h.GetWallet)
	v1.Post("/wallets/:id/deposit", h.Deposit)
	v1.Post("/wallets/:id/withdraw", h.Withdraw)
	v1.Post("/wallets/:id/transfer", h.Transfer)
	v1.Get("/wallets/:id/transactions", h.ListTransactions)
	v1.Patch("/wallets/:id/status", h.SetStatus)
	return app
}

func doRequest(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	decoded := map[string]any{}
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded
}

func sampleWallet() *domain.Wallet {
	return &domain.Wallet{
		ID:        uuid.New(),
		UserID:    "user-001",
		Balance:   15000,
		Currency:  "THB",
		Status:    domain.WalletStatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func sampleTransaction(walletID uuid.UUID) *domain.Transaction {
	return &domain.Transaction{
		ID:         uuid.New(),
		ToWalletID: &walletID,
		Amount:     15000,
		Type:       domain.TransactionTypeDeposit,
		Status:     domain.TransactionStatusCompleted,
		CreatedAt:  time.Now(),
	}
}

// --- tests ---

func TestCreateWalletValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"valid", `{"user_id":"user-001","currency":"THB"}`, 201, ""},
		{"currency omitted defaults", `{"user_id":"user-001"}`, 201, ""},
		{"malformed json", `{"user_id":`, 400, "INVALID_REQUEST"},
		{"missing user_id", `{"currency":"THB"}`, 400, "INVALID_USER_ID"},
		{"blank user_id", `{"user_id":"   ","currency":"THB"}`, 400, "INVALID_USER_ID"},
		{"user_id too long", `{"user_id":"` + strings.Repeat("x", 101) + `"}`, 400, "INVALID_USER_ID"},
		{"unsupported currency", `{"user_id":"u","currency":"XYZ"}`, 400, "UNSUPPORTED_CURRENCY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(&mockService{wallet: sampleWallet()})
			status, body := doRequest(t, app, "POST", "/api/v1/wallets", tt.body, nil)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %v)", status, tt.wantStatus, body)
			}
			if tt.wantCode != "" && body["error"] != tt.wantCode {
				t.Errorf("code = %v, want %q", body["error"], tt.wantCode)
			}
		})
	}
}

func TestWalletResponseShape(t *testing.T) {
	w := sampleWallet()
	app := newTestApp(&mockService{wallet: w})

	status, body := doRequest(t, app, "GET", "/api/v1/wallets/"+w.ID.String(), "", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}

	// Money is exposed twice: the exact integer and a preformatted string.
	if body["balance"].(float64) != 15000 {
		t.Errorf("balance = %v, want 15000", body["balance"])
	}
	if body["balance_display"] != "150.00" {
		t.Errorf("balance_display = %v, want \"150.00\"", body["balance_display"])
	}
	if body["status"] != "ACTIVE" {
		t.Errorf("status = %v, want ACTIVE", body["status"])
	}
}

func TestInvalidWalletIDIsRejected(t *testing.T) {
	app := newTestApp(&mockService{wallet: sampleWallet()})

	for _, path := range []string{
		"/api/v1/wallets/not-a-uuid",
		"/api/v1/wallets/not-a-uuid/transactions",
	} {
		status, body := doRequest(t, app, "GET", path, "", nil)
		if status != 400 || body["error"] != "INVALID_WALLET_ID" {
			t.Errorf("%s: status = %d, code = %v; want 400 INVALID_WALLET_ID", path, status, body["error"])
		}
	}
}

// A fractional amount cannot decode into int64, which is the intended
// rejection: money is only ever accepted as whole satang.
func TestFractionalAmountIsRejected(t *testing.T) {
	w := sampleWallet()
	app := newTestApp(&mockService{result: &service.TransactionResult{Transaction: sampleTransaction(w.ID)}})

	status, body := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", `{"amount":150.5}`, nil)
	if status != 400 {
		t.Errorf("status = %d, want 400", status)
	}
	if body["error"] != "INVALID_AMOUNT" {
		t.Errorf("code = %v, want INVALID_AMOUNT", body["error"])
	}
}

func TestReferenceTooLongIsRejected(t *testing.T) {
	w := sampleWallet()
	app := newTestApp(&mockService{result: &service.TransactionResult{Transaction: sampleTransaction(w.ID)}})

	body := `{"amount":100,"reference":"` + strings.Repeat("x", 256) + `"}`
	status, resp := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", body, nil)
	if status != 400 || resp["error"] != "INVALID_REFERENCE" {
		t.Errorf("status = %d, code = %v; want 400 INVALID_REFERENCE", status, resp["error"])
	}
}

func TestBusinessErrorsMapToTheirStatus(t *testing.T) {
	w := sampleWallet()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"insufficient balance", domain.ErrInsufficientBalance, 422, "INSUFFICIENT_BALANCE"},
		{"wallet inactive", domain.ErrWalletInactive, 409, "WALLET_INACTIVE"},
		{"wallet not found", domain.ErrWalletNotFound, 404, "WALLET_NOT_FOUND"},
		{"idempotency reuse", domain.ErrIdempotencyKeyReuse, 422, "IDEMPOTENCY_KEY_REUSE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(&mockService{err: tt.err})
			status, body := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/withdraw", `{"amount":100}`, nil)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if body["error"] != tt.wantCode {
				t.Errorf("code = %v, want %q", body["error"], tt.wantCode)
			}
		})
	}
}

// A fresh operation is 201; a replay of an earlier one is 200, so a client can
// tell whether its retry actually did anything.
func TestReplayedTransactionReturns200(t *testing.T) {
	w := sampleWallet()
	txn := sampleTransaction(w.ID)

	t.Run("fresh is 201", func(t *testing.T) {
		app := newTestApp(&mockService{result: &service.TransactionResult{Transaction: txn}})
		status, _ := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", `{"amount":15000}`, nil)
		if status != 201 {
			t.Errorf("status = %d, want 201", status)
		}
	})

	t.Run("replay is 200", func(t *testing.T) {
		app := newTestApp(&mockService{result: &service.TransactionResult{Transaction: txn, Replayed: true}})
		status, body := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", `{"amount":15000}`, nil)
		if status != 200 {
			t.Errorf("status = %d, want 200", status)
		}
		if body["id"] != txn.ID.String() {
			t.Errorf("id = %v, want the original transaction id", body["id"])
		}
	})
}

func TestIdempotencyKeyIsForwarded(t *testing.T) {
	w := sampleWallet()

	t.Run("header is passed through", func(t *testing.T) {
		svc := &mockService{result: &service.TransactionResult{Transaction: sampleTransaction(w.ID)}}
		app := newTestApp(svc)

		doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", `{"amount":100}`,
			map[string]string{"Idempotency-Key": "key-123"})

		if svc.gotIdempotencyKey == nil || *svc.gotIdempotencyKey != "key-123" {
			t.Errorf("key = %v, want \"key-123\"", svc.gotIdempotencyKey)
		}
	})

	// nil keeps the column NULL, and Postgres does not collide NULLs in a
	// unique index, so unkeyed requests never interfere with each other.
	t.Run("absent header becomes nil", func(t *testing.T) {
		svc := &mockService{result: &service.TransactionResult{Transaction: sampleTransaction(w.ID)}}
		app := newTestApp(svc)

		doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/deposit", `{"amount":100}`, nil)

		if svc.gotIdempotencyKey != nil {
			t.Errorf("key = %v, want nil", *svc.gotIdempotencyKey)
		}
	})
}

func TestTransferRequiresValidDestination(t *testing.T) {
	w := sampleWallet()
	app := newTestApp(&mockService{result: &service.TransactionResult{Transaction: sampleTransaction(w.ID)}})

	status, body := doRequest(t, app, "POST", "/api/v1/wallets/"+w.ID.String()+"/transfer",
		`{"to_wallet_id":"nope","amount":100}`, nil)

	if status != 400 || body["error"] != "INVALID_WALLET_ID" {
		t.Errorf("status = %d, code = %v; want 400 INVALID_WALLET_ID", status, body["error"])
	}
}

func TestListTransactionsQueryParams(t *testing.T) {
	w := sampleWallet()
	svc := &mockService{page: &service.TransactionPage{
		Items: []domain.Transaction{*sampleTransaction(w.ID)}, Limit: 10, Offset: 5, Total: 1,
	}}
	app := newTestApp(svc)

	status, body := doRequest(t, app, "GET", "/api/v1/wallets/"+w.ID.String()+"/transactions?limit=10&offset=5", "", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if svc.gotLimit != 10 || svc.gotOffset != 5 {
		t.Errorf("service got limit=%d offset=%d, want 10 and 5", svc.gotLimit, svc.gotOffset)
	}

	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("missing pagination in %v", body)
	}
	if pagination["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1", pagination["total"])
	}

	items, ok := body["data"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("data = %v, want one entry", body["data"])
	}
	if items[0].(map[string]any)["amount_display"] != "150.00" {
		t.Errorf("amount_display = %v, want \"150.00\"", items[0].(map[string]any)["amount_display"])
	}
}

func TestEmptyHistorySerialisesAsArray(t *testing.T) {
	w := sampleWallet()
	app := newTestApp(&mockService{page: &service.TransactionPage{
		Items: []domain.Transaction{}, Limit: 20, Offset: 0, Total: 0,
	}})

	status, body := doRequest(t, app, "GET", "/api/v1/wallets/"+w.ID.String()+"/transactions", "", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if _, ok := body["data"].([]any); !ok {
		t.Errorf("data = %v, want [] rather than null", body["data"])
	}
}

func TestSetStatus(t *testing.T) {
	w := sampleWallet()

	t.Run("forwards the requested status", func(t *testing.T) {
		svc := &mockService{wallet: w}
		app := newTestApp(svc)

		status, _ := doRequest(t, app, "PATCH", "/api/v1/wallets/"+w.ID.String()+"/status", `{"status":"CLOSED"}`, nil)
		if status != 200 {
			t.Errorf("status = %d, want 200", status)
		}
		if svc.gotStatus != domain.WalletStatusClosed {
			t.Errorf("service got %q, want CLOSED", svc.gotStatus)
		}
	})

	t.Run("an unknown status is a 400 from the service", func(t *testing.T) {
		app := newTestApp(&mockService{err: domain.ErrInvalidStatus})

		status, body := doRequest(t, app, "PATCH", "/api/v1/wallets/"+w.ID.String()+"/status", `{"status":"FROZEN"}`, nil)
		if status != 400 || body["error"] != "INVALID_STATUS" {
			t.Errorf("status = %d, code = %v; want 400 INVALID_STATUS", status, body["error"])
		}
	})
}
