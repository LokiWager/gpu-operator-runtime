package store

import (
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

func TestMemoryStore_StockLifecycle(t *testing.T) {
	s := NewMemoryStore()

	created, err := s.CreateStocks(2, domain.StockSpec{Name: "g1.1"})
	if err != nil {
		t.Fatalf("create stocks error: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 stocks, got %d", len(created))
	}

	reserved, err := s.ReserveStock("g1.1")
	if err != nil {
		t.Fatalf("reserve stock error: %v", err)
	}
	if reserved.Status != domain.StockStatusAllocated {
		t.Fatalf("expected allocated status, got %s", reserved.Status)
	}

	summary := s.Summary()
	if summary.TotalStocks != 2 || summary.AvailableStocks != 1 || summary.AllocatedStocks != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	if err := s.ReleaseStock(reserved.ID); err != nil {
		t.Fatalf("release stock error: %v", err)
	}

	summary = s.Summary()
	if summary.AvailableStocks != 2 || summary.AllocatedStocks != 0 {
		t.Fatalf("unexpected summary after release: %+v", summary)
	}
}

func TestMemoryStore_VMLifecycle(t *testing.T) {
	s := NewMemoryStore()
	stocks, err := s.CreateStocks(1, domain.StockSpec{Name: "g1.1"})
	if err != nil {
		t.Fatalf("create stocks error: %v", err)
	}

	stock, err := s.ReserveStockByID(stocks[0].ID)
	if err != nil {
		t.Fatalf("reserve by id error: %v", err)
	}

	vm := domain.VM{ID: "vm-1", SpecName: stock.Spec.Name, StockID: stock.ID, Status: domain.VMStatusRunning}
	if err := s.CreateVM(vm); err != nil {
		t.Fatalf("create vm error: %v", err)
	}

	_, ok := s.GetVM("vm-1")
	if !ok {
		t.Fatalf("expected vm to exist")
	}

	delVM, err := s.DeleteVM("vm-1")
	if err != nil {
		t.Fatalf("delete vm error: %v", err)
	}
	if delVM.ID != "vm-1" {
		t.Fatalf("unexpected deleted vm: %+v", delVM)
	}

	if err := s.ReleaseStock(stock.ID); err != nil {
		t.Fatalf("release stock error: %v", err)
	}
}
