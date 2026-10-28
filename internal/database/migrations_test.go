package database

import "testing"

func TestLoadMigrationsPairsUpAndDown(t *testing.T) {
	ms, err := loadMigrations("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 4 {
		t.Fatalf("got %d migrations", len(ms))
	}
	for i, m := range ms {
		if m.Up == "" || m.Down == "" {
			t.Errorf("migration %s missing up or down", m.Version)
		}
		if i > 0 && ms[i-1].Version >= m.Version {
			t.Errorf("not sorted: %s then %s", ms[i-1].Version, m.Version)
		}
	}
}
