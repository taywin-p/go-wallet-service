package handler

import (
	"errors"
	"log"

	"wallet-service/internal/domain"

	"github.com/gofiber/fiber/v2"
)

type apiError struct {
	Error   string `json:"error"`   // stable machine-readable code
	Message string `json:"message"` // human-readable explanation
}

// errorTable maps business-rule failures to HTTP responses. A slice rather
// than a map because the repository wraps errors with %w, so matching has to
// go through errors.Is rather than equality.
var errorTable = []struct {
	target error
	status int
	code   string
}{
	{domain.ErrWalletNotFound, fiber.StatusNotFound, "WALLET_NOT_FOUND"},
	{domain.ErrTransactionNotFound, fiber.StatusNotFound, "TRANSACTION_NOT_FOUND"},
	{domain.ErrWalletInactive, fiber.StatusConflict, "WALLET_INACTIVE"},
	{domain.ErrWalletAlreadyExists, fiber.StatusConflict, "WALLET_ALREADY_EXISTS"},
	{domain.ErrCurrencyMismatch, fiber.StatusConflict, "CURRENCY_MISMATCH"},
	{domain.ErrInsufficientBalance, fiber.StatusUnprocessableEntity, "INSUFFICIENT_BALANCE"},
	{domain.ErrBalanceOverflow, fiber.StatusUnprocessableEntity, "BALANCE_OVERFLOW"},
	{domain.ErrIdempotencyKeyReuse, fiber.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSE"},
	{domain.ErrInvalidAmount, fiber.StatusBadRequest, "INVALID_AMOUNT"},
	{domain.ErrSameWallet, fiber.StatusBadRequest, "SAME_WALLET"},
	{domain.ErrInvalidStatus, fiber.StatusBadRequest, "INVALID_STATUS"},
}

// respondError is the single place HTTP statuses are chosen. Handlers never
// guess, which is what stops "wallet not found" and "insufficient balance"
// from both coming back as 400.
func respondError(c *fiber.Ctx, err error) error {
	for _, e := range errorTable {
		if errors.Is(err, e.target) {
			return c.Status(e.status).JSON(apiError{Error: e.code, Message: e.target.Error()})
		}
	}

	// Anything unrecognised is a bug or an infrastructure failure: log the
	// detail, return none of it.
	log.Printf("unhandled error: %v", err)
	return c.Status(fiber.StatusInternalServerError).
		JSON(apiError{Error: "INTERNAL_ERROR", Message: "internal server error"})
}

// respondBadRequest reports a malformed request. Request shape is the
// handler's responsibility; business rules belong to the service.
func respondBadRequest(c *fiber.Ctx, code, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(apiError{Error: code, Message: message})
}
