package handler

import (
	"wallet-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type WalletHandler struct {
	service service.WalletService
}

func NewWalletHandler(s service.WalletService) *WalletHandler {
	return &WalletHandler{service: s}
}

func (h *WalletHandler) CreateWallet(c *fiber.Ctx) error {
	type Request struct {
		UserID   string `json:"user_id"`
		Currency string `json:"currency"`
	}

	var req Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	wallet, err := h.service.CreateWallet(req.UserID, req.Currency)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(wallet)
}

func (h *WalletHandler) GetWallet(c *fiber.Ctx) error {
	id := c.Params("id")
	walletID, err := uuid.Parse(id)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid wallet ID"})
	}

	wallet, err := h.service.GetWallet(walletID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Wallet not found"})
	}

	return c.JSON(wallet)
}

func (h *WalletHandler) TopUp(c *fiber.Ctx) error {
	type Request struct {
		Amount float64 `json:"amount"`
	}

	id := c.Params("id")
	walletID, err := uuid.Parse(id)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid wallet ID"})
	}

	var req Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	tx, err := h.service.TopUp(walletID, req.Amount)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(tx)
}

func (h *WalletHandler) Transfer(c *fiber.Ctx) error {
	type Request struct {
		ToWalletID string  `json:"to_wallet_id"`
		Amount     float64 `json:"amount"`
	}

	fromID := c.Params("id")
	fromWalletID, err := uuid.Parse(fromID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid source wallet ID"})
	}

	var req Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	toWalletID, err := uuid.Parse(req.ToWalletID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid destination wallet ID"})
	}

	tx, err := h.service.Transfer(fromWalletID, toWalletID, req.Amount)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(tx)
}
