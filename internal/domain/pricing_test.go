package domain

import "testing"

func TestAmountForWeight(t *testing.T) {
	// 12.5kg × 1.20元/kg = 15.00元 = 1500分
	if got := AmountForWeight(12500, 120); got != 1500 {
		t.Fatalf("AmountForWeight(12500,120)=%d, want 1500", got)
	}
	// 0.1kg × 3.50元/kg = 0.35元 = 35分
	if got := AmountForWeight(100, 350); got != 35 {
		t.Fatalf("AmountForWeight(100,350)=%d, want 35", got)
	}
	// 零重量/零价
	if got := AmountForWeight(0, 120); got != 0 {
		t.Fatalf("zero weight should be 0, got %d", got)
	}
}

func TestEffectiveWeight(t *testing.T) {
	// 10kg 扣杂 10% -> 9kg
	if got := EffectiveWeightG(10000, 100); got != 9000 {
		t.Fatalf("EffectiveWeightG(10000,100)=%d, want 9000", got)
	}
	if got := EffectiveWeightG(10000, 0); got != 10000 {
		t.Fatalf("no contamination should keep weight, got %d", got)
	}
	if got := EffectiveWeightG(10000, 1000); got != 0 {
		t.Fatalf("full contamination should be 0, got %d", got)
	}
}

func TestPointsForAmount(t *testing.T) {
	// 15.00元 × 10积分/元 × 1.1 = 165 积分
	if got := PointsForAmount(1500, 10, 1100); got != 165 {
		t.Fatalf("PointsForAmount(1500,10,1100)=%d, want 165", got)
	}
	// 无激励: 1500分 × 10 / 100 = 150
	if got := PointsForAmount(1500, 10, 1000); got != 150 {
		t.Fatalf("PointsForAmount(1500,10,1000)=%d, want 150", got)
	}
}

func TestUnitConversion(t *testing.T) {
	if KgToGrams(12.5) != 12500 {
		t.Fatalf("KgToGrams(12.5)=%d", KgToGrams(12.5))
	}
	if YuanToCents(1.2) != 120 {
		t.Fatalf("YuanToCents(1.2)=%d", YuanToCents(1.2))
	}
	if CentsToYuan(1500) != 15.0 {
		t.Fatalf("CentsToYuan(1500)=%f", CentsToYuan(1500))
	}
}

func TestVarianceTolerance(t *testing.T) {
	// 10kg 现场, 复称 10.3kg: 差 300g < 500g -> 不超差
	if IsVarianceOverTolerance(10000, 10300) {
		t.Fatal("300g diff should be within tolerance")
	}
	// 10kg 现场, 复称 9kg: 差 1kg > 500g 且 10% > 5% -> 超差
	if !IsVarianceOverTolerance(10000, 9000) {
		t.Fatal("1kg/10% diff should be over tolerance")
	}
	// 1kg 现场, 复称 400g: 差 600g > 500g 且 60% > 5% -> 超差
	if !IsVarianceOverTolerance(1000, 400) {
		t.Fatal("600g/60% diff should be over tolerance")
	}
	// 20kg 现场, 复称 19.2kg: 差 800g > 500g 但 4% < 5% -> 不超差
	if IsVarianceOverTolerance(20000, 19200) {
		t.Fatal("800g/4% diff should be within tolerance")
	}
}

func TestCollectorEarning(t *testing.T) {
	// 20kg, 5 楼无电梯, 2km: 300 + 20*20 + (5-3)*50 + 2*100 = 300+400+100+200=1000
	base, weight, floor, dist, total := ComputeCollectorEarning(20000, 5, false, 2.0)
	if base != 300 || weight != 400 || floor != 100 || dist != 200 || total != 1000 {
		t.Fatalf("earning=%d,%d,%d,%d,%d want 300,400,100,200,1000", base, weight, floor, dist, total)
	}
	// 有电梯无楼层补贴
	_, _, floor2, _, _ := ComputeCollectorEarning(20000, 9, true, 2.0)
	if floor2 != 0 {
		t.Fatalf("elevator should have no floor bonus, got %d", floor2)
	}
}

func TestCO2(t *testing.T) {
	// 10kg 废纸 × 1170g/kg = 11700g
	if got := CO2SavedG(10000, 1170); got != 11700 {
		t.Fatalf("CO2SavedG=%d, want 11700", got)
	}
}
