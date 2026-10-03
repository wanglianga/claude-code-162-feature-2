package domain

import "testing"

func TestHazmatCatalog(t *testing.T) {
	h, ok := LookupHazmat("paint_bucket")
	if !ok {
		t.Fatal("paint_bucket must exist in catalog")
	}
	if h.Name != "油漆桶" {
		t.Fatalf("name=%s want 油漆桶", h.Name)
	}
	if h.RiskLevel != RiskHigh {
		t.Fatalf("paint bucket risk=%s want HIGH", h.RiskLevel)
	}
	// 油漆桶常被混入金属
	if !h.CanMixInCategory(CatMetal) {
		t.Fatal("paint bucket should mix in metal")
	}
	if h.CanMixInCategory(CatPaper) {
		t.Fatal("paint bucket should not mix in paper")
	}
	if _, ok := LookupHazmat("not_a_hazmat"); ok {
		t.Fatal("unknown code must not be found")
	}
}

func TestRefusalBases(t *testing.T) {
	h, _ := LookupHazmat("paint_bucket")
	code, text := h.PrimaryRefusal()
	if code != BasisNationalHWList {
		t.Fatalf("primary basis=%s want NATIONAL_HW_LIST", code)
	}
	if text == "" || text != RefusalBasisText(code) {
		t.Fatal("primary basis text must be populated")
	}
	bases := h.AllRefusalBases()
	if len(bases) < 2 {
		t.Fatalf("paint bucket should cite multiple bases, got %d", len(bases))
	}
	for _, b := range bases {
		if b["code"] == "" || b["text"] == "" {
			t.Fatal("each basis needs code and text")
		}
	}
	// 居民坚持要求带走时, 必须能给出"无危废许可"依据
	found := false
	for _, b := range bases {
		if b["code"] == BasisNoLicense {
			found = true
		}
	}
	if !found {
		t.Fatal("paint bucket refusal must cite NO_HAZMAT_LICENSE")
	}
}

func TestHazmatAdvice(t *testing.T) {
	// 每个目录项都必须有正确投放建议(拒收后向居民展示)
	for _, code := range []string{"paint_bucket", "used_oil_drum", "lead_battery", "fluorescent_lamp", "pesticide_container", "expired_medicine"} {
		h, ok := LookupHazmat(code)
		if !ok {
			t.Fatalf("catalog missing %s", code)
		}
		if h.Advice == "" {
			t.Fatalf("%s missing disposal advice", code)
		}
		if h.RiskNote == "" {
			t.Fatalf("%s missing risk note", code)
		}
		if len(h.Bases) == 0 {
			t.Fatalf("%s missing refusal bases", code)
		}
	}
}
