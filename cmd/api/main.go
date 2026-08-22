package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"wallet-service/internal/config"
	"wallet-service/internal/handler"
	"wallet-service/internal/repository"
	"wallet-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

func main() {
	cfg := config.Load()

	db := repository.NewDatabase(cfg)
	walletRepo := repository.NewWalletRepository(db.DB)
	walletService := service.NewWalletService(walletRepo)
	walletHandler := handler.NewWalletHandler(walletService)

	app := fiber.New(fiber.Config{
		AppName: "wallet-service",
	})

	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} ${latency} ${method} ${path} ${locals:requestid}\n",
	}))

	// Liveness: is the process up? Deliberately does not touch the database, so
	// a DB outage never gets the container killed and restarted pointlessly.
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Readiness: can we actually serve traffic? A load balancer should pull us
	// out of rotation while the database is unreachable.
	app.Get("/health/ready", func(c *fiber.Ctx) error {
		sqlDB, err := db.DB.DB()
		if err == nil {
			err = sqlDB.PingContext(c.UserContext())
		}
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).
				JSON(fiber.Map{"status": "unavailable", "error": "database unreachable"})
		}
		return c.JSON(fiber.Map{"status": "ready"})
	})

	api := app.Group("/api")
	v1 := api.Group("/v1")

	v1.Post("/wallets", walletHandler.CreateWallet)                     // 1. create a wallet
	v1.Get("/wallets/:id", walletHandler.GetWallet)                     // 2. current balance
	v1.Post("/wallets/:id/deposit", walletHandler.Deposit)              // 3. deposit
	v1.Post("/wallets/:id/withdraw", walletHandler.Withdraw)            // 4. withdraw
	v1.Post("/wallets/:id/transfer", walletHandler.Transfer)            // 5. transfer
	v1.Get("/wallets/:id/transactions", walletHandler.ListTransactions) // 6. transaction history
	v1.Patch("/wallets/:id/status", walletHandler.SetStatus)            // 7. open / close account

	// Stop accepting new requests on SIGINT/SIGTERM but let in-flight database
	// transactions finish, so `docker compose down` never severs a transfer.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := app.Listen(":" + cfg.AppPort); err != nil {
			log.Fatal("Failed to start server: ", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutdown signal received, draining connections...")
	if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
		log.Print("Graceful shutdown failed: ", err)
	}
	log.Println("Server stopped")
}
