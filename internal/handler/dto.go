package handler

import (
	"strings"

	"wallet-service/internal/domain"
	"wallet-service/internal/service"

	"github.com/google/uuid"
)

// Column widths from the domain model. Validating here turns an oversized
// field into a clean 400 instead of letting Postgres reject it as a 500.
const (
	maxUserIDLength    = 100
	maxReferenceLength = 255
)

// supportedCurrencies is an allowlist rather than a "three letters" check, so
// "what currencies does this support?" has a definite answer.
var supportedCurrencies = map[string]bool{"THB": true, "USD": true, "EUR": true}

// --- requests ---

type createWalletRequest struct {
	UserID   string `json:"user_id"`
	Currency string `json:"currency"`
}

// amountRequest is shared by deposit and withdraw. Amount is an integer in
// satang; a fractional value like 150.5 fails to decode, which is the intended
// rejection rather than a silent truncation.
type amountRequest struct {
	Amount    int64  `json:"amount"`
	Reference string `json:"reference"`
}

type transferRequest struct {
	ToWalletID string `json:"to_wallet_id"`
	Amount     int64  `json:"amount"`
	Reference  string `json:"reference"`
}

type setStatusRequest struct {
	Status string `json:"status"`
}

// --- responses ---

// walletResponse exposes money twice: the exact integer the system reasons
// about, and a preformatted string so clients never do decimal arithmetic in
// floating point to display it.
type walletResponse struct {
	ID             uuid.UUID `json:"id"`
	UserID         string    `json:"user_id"`
	Balance        int64     `json:"balance"`
	BalanceDisplay string    `json:"balance_display"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
}

type transactionResponse struct {
	ID             uuid.UUID  `json:"id"`
	FromWalletID   *uuid.UUID `json:"from_wallet_id"`
	ToWalletID     *uuid.UUID `json:"to_wallet_id"`
	Amount         int64      `json:"amount"`
	AmountDisplay  string     `json:"amount_display"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	Reference      string     `json:"reference,omitempty"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	CreatedAt      string     `json:"created_at"`
}

type paginationResponse struct {
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
	Total  int64 `json:"total"`
}

type transactionListResponse struct {
	Data       []transactionResponse `json:"data"`
	Pagination paginationResponse    `json:"pagination"`
}

const timeFormat = "2006-01-02T15:04:05Z07:00"

func toWalletResponse(w *domain.Wallet) walletResponse {
	return walletResponse{
		ID:             w.ID,
		UserID:         w.UserID,
		Balance:        w.Balance,
		BalanceDisplay: domain.FormatSatang(w.Balance),
		Currency:       w.Currency,
		Status:         string(w.Status),
		CreatedAt:      w.CreatedAt.Format(timeFormat),
		UpdatedAt:      w.UpdatedAt.Format(timeFormat),
	}
}

func toTransactionResponse(t *domain.Transaction) transactionResponse {
	return transactionResponse{
		ID:             t.ID,
		FromWalletID:   t.FromWalletID,
		ToWalletID:     t.ToWalletID,
		Amount:         t.Amount,
		AmountDisplay:  domain.FormatSatang(t.Amount),
		Type:           string(t.Type),
		Status:         string(t.Status),
		Reference:      t.Reference,
		IdempotencyKey: t.IdempotencyKey,
		CreatedAt:      t.CreatedAt.Format(timeFormat),
	}
}

func toTransactionListResponse(page *service.TransactionPage) transactionListResponse {
	// Non-nil so an empty history serialises as [] rather than null.
	items := make([]transactionResponse, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, toTransactionResponse(&page.Items[i]))
	}
	return transactionListResponse{
		Data: items,
		Pagination: paginationResponse{
			Limit:  page.Limit,
			Offset: page.Offset,
			Total:  page.Total,
		},
	}
}

// --- validation ---

// validate checks request shape only. It normalises the currency so the
// allowlist and the stored value agree.
func (r *createWalletRequest) validate() (code, message string, ok bool) {
	r.UserID = strings.TrimSpace(r.UserID)
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))

	if r.UserID == "" {
		return "INVALID_USER_ID", "user_id is required", false
	}
	if len(r.UserID) > maxUserIDLength {
		return "INVALID_USER_ID", "user_id must be at most 100 characters", false
	}
	if r.Currency == "" {
		r.Currency = "THB"
	}
	if !supportedCurrencies[r.Currency] {
		return "UNSUPPORTED_CURRENCY", "currency must be one of THB, USD, EUR", false
	}
	return "", "", true
}

func validateReference(reference string) (code, message string, ok bool) {
	if len(reference) > maxReferenceLength {
		return "INVALID_REFERENCE", "reference must be at most 255 characters", false
	}
	return "", "", true
}
