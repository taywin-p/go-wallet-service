package domain

import "errors"

// Business-rule failures. Request-shape problems (unparseable UUID, missing
// user_id, oversized reference) are rejected in the handler and never reach
// here -- these are decisions the service layer makes about valid input.
var (
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrWalletInactive      = errors.New("wallet is not active")
	ErrWalletAlreadyExists = errors.New("wallet already exists for this user and currency")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrInvalidAmount       = errors.New("amount must be a positive integer in satang")
	ErrBalanceOverflow     = errors.New("resulting balance exceeds the maximum supported value")
	ErrCurrencyMismatch    = errors.New("currency mismatch")
	ErrSameWallet          = errors.New("cannot transfer to the same wallet")
	ErrInvalidStatus       = errors.New("invalid wallet status")
	ErrIdempotencyKeyReuse = errors.New("idempotency key was already used for a different operation")
)

// ErrDuplicateIdempotencyKey is an internal signal, not an API error. The
// repository raises it when a unique-index violation proves the operation was
// already applied; the service catches it and returns the original record.
var ErrDuplicateIdempotencyKey = errors.New("duplicate idempotency key")
