package service

import (
	"errors"
	"wallet-service/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WalletService interface {
	CreateWallet(userID string, currency string) (*domain.Wallet, error)
	GetWallet(walletID uuid.UUID) (*domain.Wallet, error)
	TopUp(walletID uuid.UUID, amount float64) (*domain.Transaction, error)
	Transfer(fromWalletID uuid.UUID, toWalletID uuid.UUID, amount float64) (*domain.Transaction, error)
}

type walletService struct {
	db *gorm.DB
}

func NewWalletService(db *gorm.DB) WalletService {
	return &walletService{db: db}
}

func (s *walletService) CreateWallet(userID string, currency string) (*domain.Wallet, error) {
	wallet := &domain.Wallet{
		UserID:   userID,
		Currency: currency,
		Balance:  0,
	}

	if err := s.db.Create(wallet).Error; err != nil {
		return nil, err
	}
	return wallet, nil
}

func (s *walletService) GetWallet(walletID uuid.UUID) (*domain.Wallet, error) {
	var wallet domain.Wallet
	if err := s.db.First(&wallet, "id = ?", walletID).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (s *walletService) TopUp(walletID uuid.UUID, amount float64) (*domain.Transaction, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be greater than 0")
	}

	tx := s.db.Begin()

	// 1. Update Wallet Balance
	var wallet domain.Wallet
	if err := tx.First(&wallet, "id = ?", walletID).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	wallet.Balance += amount
	if err := tx.Save(&wallet).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 2. Create Transaction Record
	transaction := &domain.Transaction{
		ToWalletID: &walletID,
		Amount:     amount,
		Type:       domain.TransactionTypeDeposit,
		Status:     domain.TransactionStatusCompleted,
	}

	if err := tx.Create(transaction).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	tx.Commit()
	return transaction, nil
}

func (s *walletService) Transfer(fromWalletID uuid.UUID, toWalletID uuid.UUID, amount float64) (*domain.Transaction, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be greater than 0")
	}
	if fromWalletID == toWalletID {
		return nil, errors.New("cannot transfer to self")
	}

	tx := s.db.Begin()

	// 1. Deduct from Sender
	var sender domain.Wallet
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sender, "id = ?", fromWalletID).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if sender.Balance < amount {
		tx.Rollback()
		return nil, errors.New("insufficient balance")
	}

	sender.Balance -= amount
	if err := tx.Save(&sender).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 2. Add to Receiver
	var receiver domain.Wallet
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&receiver, "id = ?", toWalletID).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	
	if sender.Currency != receiver.Currency {
		tx.Rollback()
		return nil, errors.New("currency mismatch (exchange not supported)")
	}

	receiver.Balance += amount
	if err := tx.Save(&receiver).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 3. Create Transaction Record
	transaction := &domain.Transaction{
		FromWalletID: &fromWalletID,
		ToWalletID:   &toWalletID,
		Amount:       amount,
		Type:         domain.TransactionTypeTransfer,
		Status:       domain.TransactionStatusCompleted,
	}

	if err := tx.Create(transaction).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	tx.Commit()
	return transaction, nil
}
