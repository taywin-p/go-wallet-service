package service

import (
	"context"
	"errors"

	"wallet-service/internal/domain"

	"github.com/google/uuid"
)

// mockRepo is a hand-written stand-in for domain.Repository. It is a _test.go
// file, so it never ships in the binary, and it is checked against the
// interface by the compiler rather than by a reflection-based DSL.
//
// Two fields do work a generated mock could not:
//
//   - inTx records whether we are inside WithTx. Mutations assert on it, so the
//     suite proves every balance change happens inside a transaction without
//     ever touching a database.
//   - lockOrder records the sequence of GetWalletForUpdate calls, which is how
//     the deterministic lock ordering that prevents deadlock gets tested.
type mockRepo struct {
	wallets      map[uuid.UUID]*domain.Wallet
	transactions []domain.Transaction
	byKey        map[string]*domain.Transaction

	inTx      bool
	lockOrder []uuid.UUID
	callOrder []string

	// Optional overrides for exercising failure paths.
	createWalletErr       error
	createTransactionErr  error
	updateWalletErr       error
	getWalletForUpdateErr error
	listErr               error
	countErr              error
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		wallets: make(map[uuid.UUID]*domain.Wallet),
		byKey:   make(map[string]*domain.Transaction),
	}
}

func (m *mockRepo) addWallet(w *domain.Wallet) *domain.Wallet {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	if w.Status == "" {
		w.Status = domain.WalletStatusActive
	}
	if w.Currency == "" {
		w.Currency = "THB"
	}
	m.wallets[w.ID] = w
	return w
}

// errNotInTransaction is what makes the "every mutation is transactional"
// guarantee testable: a write outside WithTx fails the test outright.
var errNotInTransaction = errors.New("mutation attempted outside a transaction")

func (m *mockRepo) WithTx(ctx context.Context, fn func(domain.Repository) error) error {
	m.inTx = true
	defer func() { m.inTx = false }()
	return fn(m)
}

func (m *mockRepo) CreateWallet(ctx context.Context, w *domain.Wallet) error {
	m.callOrder = append(m.callOrder, "CreateWallet")
	if m.createWalletErr != nil {
		return m.createWalletErr
	}
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	m.wallets[w.ID] = w
	return nil
}

func (m *mockRepo) GetWallet(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	m.callOrder = append(m.callOrder, "GetWallet")
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	clone := *w
	return &clone, nil
}

func (m *mockRepo) GetWalletForUpdate(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	m.callOrder = append(m.callOrder, "GetWalletForUpdate")
	m.lockOrder = append(m.lockOrder, id)
	if m.getWalletForUpdateErr != nil {
		return nil, m.getWalletForUpdateErr
	}
	if !m.inTx {
		return nil, errNotInTransaction
	}
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	clone := *w
	return &clone, nil
}

func (m *mockRepo) UpdateWallet(ctx context.Context, w *domain.Wallet) error {
	m.callOrder = append(m.callOrder, "UpdateWallet")
	if !m.inTx {
		return errNotInTransaction
	}
	if m.updateWalletErr != nil {
		return m.updateWalletErr
	}
	clone := *w
	m.wallets[w.ID] = &clone
	return nil
}

func (m *mockRepo) CreateTransaction(ctx context.Context, t *domain.Transaction) error {
	m.callOrder = append(m.callOrder, "CreateTransaction")
	if !m.inTx {
		return errNotInTransaction
	}
	if m.createTransactionErr != nil {
		return m.createTransactionErr
	}
	if t.IdempotencyKey != nil {
		if _, exists := m.byKey[*t.IdempotencyKey]; exists {
			// Stands in for the Postgres unique-index violation.
			return domain.ErrDuplicateIdempotencyKey
		}
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	clone := *t
	m.transactions = append(m.transactions, clone)
	if t.IdempotencyKey != nil {
		m.byKey[*t.IdempotencyKey] = &clone
	}
	return nil
}

func (m *mockRepo) ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]domain.Transaction, error) {
	m.callOrder = append(m.callOrder, "ListTransactions")
	if m.listErr != nil {
		return nil, m.listErr
	}

	matching := []domain.Transaction{}
	for _, t := range m.transactions {
		if (t.FromWalletID != nil && *t.FromWalletID == walletID) ||
			(t.ToWalletID != nil && *t.ToWalletID == walletID) {
			matching = append(matching, t)
		}
	}

	if offset >= len(matching) {
		return []domain.Transaction{}, nil
	}
	end := offset + limit
	if end > len(matching) {
		end = len(matching)
	}
	return matching[offset:end], nil
}

func (m *mockRepo) CountTransactions(ctx context.Context, walletID uuid.UUID) (int64, error) {
	m.callOrder = append(m.callOrder, "CountTransactions")
	if m.countErr != nil {
		return 0, m.countErr
	}
	var total int64
	for _, t := range m.transactions {
		if (t.FromWalletID != nil && *t.FromWalletID == walletID) ||
			(t.ToWalletID != nil && *t.ToWalletID == walletID) {
			total++
		}
	}
	return total, nil
}

func (m *mockRepo) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error) {
	m.callOrder = append(m.callOrder, "GetTransactionByIdempotencyKey")
	t, ok := m.byKey[key]
	if !ok {
		return nil, domain.ErrTransactionNotFound
	}
	clone := *t
	return &clone, nil
}

// balanceOf reads a wallet balance directly, bypassing the interface.
func (m *mockRepo) balanceOf(id uuid.UUID) int64 {
	w, ok := m.wallets[id]
	if !ok {
		return -1
	}
	return w.Balance
}

// Compile-time proof that the mock satisfies the port.
var _ domain.Repository = (*mockRepo)(nil)
