package hostsetup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveBudget(t *testing.T) {
	old := budgetUnitDir
	budgetUnitDir = t.TempDir()
	t.Cleanup(func() { budgetUnitDir = old })
	var said []string
	log := func(s string) { said = append(said, s) }

	if err := RemoveBudget(log); err != nil || len(said) != 0 {
		t.Errorf("with no unit: %v %q", err, said)
	}
	path := filepath.Join(budgetUnitDir, BudgetUnitName)
	if err := os.WriteFile(path, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBudget(log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the unit is still there: %v", err)
	}
	if len(said) != 1 {
		t.Errorf("said %q, want one line", said)
	}
}
