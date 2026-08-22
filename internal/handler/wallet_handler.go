package handler

import (
	"strconv"

	"wallet-service/internal/domain"
	"wallet-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// idempotencyHeader lets a client retry a money-moving request safely after a
// timeout or a crash without applying it twice.
const idempotencyHeader = "Idempotency-Key"

type WalletHandler struct {
	service service.WalletService
}

func NewWalletHandler(s service.WalletService) *WalletHandler {
	return &WalletHandler{service: s}
}

func (h *WalletHandler) CreateWallet(c *fiber.Ctx) error {
	var req createWalletRequest
	if err := c.BodyParser(&req); err != nil {
		return respondBadRequest(c, "INVALID_REQUEST", "request body is not valid JSON")
	}
	if code, msg, ok := req.validate(); !ok {
		return respondBadRequest(c, code, msg)
	}

	wallet, err := h.service.CreateWallet(c.UserContext(), req.UserID, req.Currency)
	if err != nil {
		return respondError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(toWalletResponse(wallet))
}

// GetWallet doubles as the current-balance endpoint: the wallet representation
// already carries balance, currency and status.
func (h *WalletHandler) GetWallet(c *fiber.Ctx) error {
	walletID, err := parseWalletID(c, "id")
	if err != nil {
		return respondBadRequest(c, "INVALID_WALLET_ID", "wallet id must be a valid UUID")
	}

	wallet, err := h.service.GetWallet(c.UserContext(), walletID)
	if err != nil {
		return respondError(c, err)
	}
	return c.JSON(toWalletResponse(wallet))
}

func (h *WalletHandler) Deposit(c *fiber.Ctx) error {
	walletID, req, ok, resp := h.parseAmountRequest(c)
	if !ok {
		return resp
	}

	result, err := h.service.Deposit(c.UserContext(), walletID, req.Amount, req.Reference, idempotencyKey(c))
	if err != nil {
		return respondError(c, err)
	}
	return respondTransaction(c, result)
}

func (h *WalletHandler) Withdraw(c *fiber.Ctx) error {
	walletID, req, ok, resp := h.parseAmountRequest(c)
	if !ok {
		return resp
	}

	result, err := h.service.Withdraw(c.UserContext(), walletID, req.Amount, req.Reference, idempotencyKey(c))
	if err != nil {
		return respondError(c, err)
	}
	return respondTransaction(c, result)
}

func (h *WalletHandler) Transfer(c *fiber.Ctx) error {
	fromWalletID, err := parseWalletID(c, "id")
	if err != nil {
		return respondBadRequest(c, "INVALID_WALLET_ID", "source wallet id must be a valid UUID")
	}

	var req transferRequest
	if err := c.BodyParser(&req); err != nil {
		return respondBadRequest(c, "INVALID_REQUEST", "request body is not valid JSON; amount must be an integer in satang")
	}
	if code, msg, ok := validateReference(req.Reference); !ok {
		return respondBadRequest(c, code, msg)
	}

	toWalletID, err := uuid.Parse(req.ToWalletID)
	if err != nil {
		return respondBadRequest(c, "INVALID_WALLET_ID", "to_wallet_id must be a valid UUID")
	}

	result, err := h.service.Transfer(c.UserContext(), fromWalletID, toWalletID, req.Amount, req.Reference, idempotencyKey(c))
	if err != nil {
		return respondError(c, err)
	}
	return respondTransaction(c, result)
}

func (h *WalletHandler) ListTransactions(c *fiber.Ctx) error {
	walletID, err := parseWalletID(c, "id")
	if err != nil {
		return respondBadRequest(c, "INVALID_WALLET_ID", "wallet id must be a valid UUID")
	}

	// Unparseable values fall back to the service defaults rather than erroring.
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	page, err := h.service.ListTransactions(c.UserContext(), walletID, limit, offset)
	if err != nil {
		return respondError(c, err)
	}
	return c.JSON(toTransactionListResponse(page))
}

func (h *WalletHandler) SetStatus(c *fiber.Ctx) error {
	walletID, err := parseWalletID(c, "id")
	if err != nil {
		return respondBadRequest(c, "INVALID_WALLET_ID", "wallet id must be a valid UUID")
	}

	var req setStatusRequest
	if err := c.BodyParser(&req); err != nil {
		return respondBadRequest(c, "INVALID_REQUEST", "request body is not valid JSON")
	}

	wallet, err := h.service.SetStatus(c.UserContext(), walletID, domain.WalletStatus(req.Status))
	if err != nil {
		return respondError(c, err)
	}
	return c.JSON(toWalletResponse(wallet))
}

// --- shared helpers ---

func parseWalletID(c *fiber.Ctx, param string) (uuid.UUID, error) {
	return uuid.Parse(c.Params(param))
}

// parseAmountRequest handles the shape shared by deposit and withdraw.
//
// ok reports whether parsing succeeded; when it is false the error response
// has already been written and resp is what the caller should return. The
// explicit bool matters: c.Status().JSON() returns nil on success, so a
// response value alone cannot signal failure.
func (h *WalletHandler) parseAmountRequest(c *fiber.Ctx) (walletID uuid.UUID, req amountRequest, ok bool, resp error) {
	walletID, err := parseWalletID(c, "id")
	if err != nil {
		return uuid.Nil, req, false, respondBadRequest(c, "INVALID_WALLET_ID", "wallet id must be a valid UUID")
	}
	if err := c.BodyParser(&req); err != nil {
		return uuid.Nil, req, false, respondBadRequest(c, "INVALID_AMOUNT", "amount must be an integer in satang (100 = 1.00)")
	}
	if code, msg, valid := validateReference(req.Reference); !valid {
		return uuid.Nil, req, false, respondBadRequest(c, code, msg)
	}
	return walletID, req, true, nil
}

// idempotencyKey returns the header value, or nil when the client did not send
// one. nil keeps the column NULL, and Postgres does not collide NULLs in a
// unique index, so unkeyed requests are unaffected by each other.
func idempotencyKey(c *fiber.Ctx) *string {
	key := c.Get(idempotencyHeader)
	if key == "" {
		return nil
	}
	return &key
}

// respondTransaction returns 201 for work performed now and 200 for a replayed
// result, so a client can tell whether its retry actually did anything.
func respondTransaction(c *fiber.Ctx, result *service.TransactionResult) error {
	status := fiber.StatusCreated
	if result.Replayed {
		status = fiber.StatusOK
	}
	return c.Status(status).JSON(toTransactionResponse(result.Transaction))
}
