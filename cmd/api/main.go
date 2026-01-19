package main

import (
	"log"
	"wallet-service/internal/handler"
	"wallet-service/internal/repository"
	"wallet-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

func main() {
	// 1. Initialize Database
	db := repository.NewDatabase()
	
	// 2. Initialize Service
	walletService := service.NewWalletService(db.DB)
	
	// 3. Initialize Handler
	walletHandler := handler.NewWalletHandler(walletService)

	// 4. Initialize Fiber
	app := fiber.New()

	// 5. Setup Routes
	api := app.Group("/api")
	v1 := api.Group("/v1")

	v1.Post("/wallets", walletHandler.CreateWallet)
	v1.Get("/wallets/:id", walletHandler.GetWallet)
	v1.Post("/wallets/:id/topup", walletHandler.TopUp)
	v1.Post("/wallets/:id/transfer", walletHandler.Transfer)

	// 6. Start Server
	log.Fatal(app.Listen(":8080"))
}
