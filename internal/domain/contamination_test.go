package domain

import "testing"

func TestAllocateHandlingCost(t *testing.T) {
	cases := []struct {
		name          string
		cost, earning int64
		wantOrder     int64
		wantTraining  int64
	}{
		{"zero cost", 0, 1000, 0, 0},
		{"fully covered by order", 500, 1000, 500, 0},
		{"exact earning", 1000, 1000, 1000, 0},
		{"split: earning insufficient", 1500, 1000, 1000, 500},
		{"no earning, all to training", 800, 0, 0, 800},
		{"negative earning treated as zero", 800, -10, 0, 800},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotO, gotT := AllocateHandlingCost(c.cost, c.earning)
			if gotO != c.wantOrder || gotT != c.wantTraining {
				t.Fatalf("AllocateHandlingCost(%d,%d)=(%d,%d), want (%d,%d)",
					c.cost, c.earning, gotO, gotT, c.wantOrder, c.wantTraining)
			}
			if gotO+gotT != c.cost {
				t.Fatalf("parts sum %d != cost %d", gotO+gotT, c.cost)
			}
		})
	}
}
