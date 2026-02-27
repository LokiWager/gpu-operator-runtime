package store

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

type MemoryStore struct {
	mu     sync.RWMutex
	stocks map[string]*domain.Stock
	vms    map[string]*domain.VM
	seq    uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		stocks: map[string]*domain.Stock{},
		vms:    map[string]*domain.VM{},
	}
}

func (s *MemoryStore) CreateStocks(number int, spec domain.StockSpec) ([]domain.Stock, error) {
	if number <= 0 {
		return nil, fmt.Errorf("number should be > 0")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("spec name is required")
	}

	now := time.Now().UTC()
	created := make([]domain.Stock, 0, number)

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 0; i < number; i++ {
		id := s.nextID("stock")
		stock := &domain.Stock{
			ID:        id,
			Spec:      spec,
			Status:    domain.StockStatusAvailable,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.stocks[id] = stock
		created = append(created, *stock)
	}

	return created, nil
}

func (s *MemoryStore) DeleteStocks(number int, specName string) ([]string, error) {
	if number <= 0 {
		return nil, fmt.Errorf("number should be > 0")
	}
	if strings.TrimSpace(specName) == "" {
		return nil, fmt.Errorf("spec name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, number)
	for id, stock := range s.stocks {
		if stock.Spec.Name == specName && stock.Status == domain.StockStatusAvailable {
			ids = append(ids, id)
			if len(ids) == number {
				break
			}
		}
	}

	if len(ids) < number {
		return nil, fmt.Errorf("insufficient available stocks for spec %s", specName)
	}

	for _, id := range ids {
		delete(s.stocks, id)
	}
	return ids, nil
}

func (s *MemoryStore) ListStocks() []domain.Stock {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]domain.Stock, 0, len(s.stocks))
	for _, stock := range s.stocks {
		out = append(out, *stock)
	}
	return out
}

func (s *MemoryStore) ReserveStock(specName string) (domain.Stock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, stock := range s.stocks {
		if stock.Spec.Name == specName && stock.Status == domain.StockStatusAvailable {
			stock.Status = domain.StockStatusAllocated
			stock.UpdatedAt = time.Now().UTC()
			return *stock, nil
		}
	}

	return domain.Stock{}, fmt.Errorf("no available stock for spec %s", specName)
}

func (s *MemoryStore) ReserveStockByID(id string) (domain.Stock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stock, ok := s.stocks[id]
	if !ok {
		return domain.Stock{}, fmt.Errorf("stock %s not found", id)
	}
	if stock.Status != domain.StockStatusAvailable {
		return domain.Stock{}, fmt.Errorf("stock %s is not available", id)
	}

	stock.Status = domain.StockStatusAllocated
	stock.UpdatedAt = time.Now().UTC()
	return *stock, nil
}

func (s *MemoryStore) ReleaseStock(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stock, ok := s.stocks[id]
	if !ok {
		return fmt.Errorf("stock %s not found", id)
	}
	stock.Status = domain.StockStatusAvailable
	stock.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemoryStore) CreateVM(vm domain.VM) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.vms[vm.ID]; ok {
		return fmt.Errorf("vm %s already exists", vm.ID)
	}
	v := vm
	s.vms[vm.ID] = &v
	return nil
}

func (s *MemoryStore) GetVM(id string) (domain.VM, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vm, ok := s.vms[id]
	if !ok {
		return domain.VM{}, false
	}
	return *vm, true
}

func (s *MemoryStore) DeleteVM(id string) (domain.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	vm, ok := s.vms[id]
	if !ok {
		return domain.VM{}, fmt.Errorf("vm %s not found", id)
	}
	delete(s.vms, id)
	return *vm, nil
}

func (s *MemoryStore) ListVMs() []domain.VM {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]domain.VM, 0, len(s.vms))
	for _, vm := range s.vms {
		out = append(out, *vm)
	}
	return out
}

func (s *MemoryStore) Summary() domain.RuntimeSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	summary := domain.RuntimeSummary{}
	summary.TotalStocks = len(s.stocks)
	summary.TotalVMs = len(s.vms)

	for _, stock := range s.stocks {
		switch stock.Status {
		case domain.StockStatusAvailable:
			summary.AvailableStocks++
		case domain.StockStatusAllocated:
			summary.AllocatedStocks++
		}
	}

	for _, vm := range s.vms {
		if vm.Status == domain.VMStatusRunning {
			summary.RunningVMs++
		}
	}
	return summary
}

func (s *MemoryStore) nextID(prefix string) string {
	seq := atomic.AddUint64(&s.seq, 1)
	return fmt.Sprintf("%s-%d", prefix, seq)
}
