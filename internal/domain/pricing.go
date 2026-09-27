package domain

import "math"

// ---- 定价与积分规则(纯函数, 便于测试与审计) ----
//
// 价格透明原则:
//   - 居民单价 = 下单时刻市场行情(分/kg), 快照锁定在 order_items.est_price_cents,
//     现场称重与站点复称均沿用该单价, 不随行就市变动。
//   - 预估金额 = 预计重量 × 锁定单价; 现场金额 = 现场重量 × (1-扣杂率) × 锁定单价;
//     最终金额 = 站点复称重量 × (1-扣杂率) × 锁定单价。
//   - 积分结算: 1 元 = points_per_yuan 积分, 选择积分给予 bonus 激励(默认 +10%)。

// AmountForWeight 金额(分) = 重量(克) × 单价(分/kg) / 1000, 四舍五入
func AmountForWeight(weightG, priceCentsPerKg int64) int64 {
	if weightG <= 0 || priceCentsPerKg <= 0 {
		return 0
	}
	num := weightG * priceCentsPerKg
	return (num + 500) / 1000
}

// EffectiveWeightG 扣除混入不可回收物后的有效重量(克)
// contaminationPM 为千分比, 如 100 表示 10%
func EffectiveWeightG(weightG int64, contaminationPM int) int64 {
	if contaminationPM <= 0 {
		return weightG
	}
	if contaminationPM >= 1000 {
		return 0
	}
	return weightG * int64(1000-contaminationPM) / 1000
}

// PointsForAmount 积分 = 金额(分) × 每元积分 × 激励千分比 / (100 × 1000)
// bonusPM: 1000 表示无激励, 1100 表示 +10%
func PointsForAmount(amountCents, pointsPerYuan int64, bonusPM int64) int64 {
	if amountCents <= 0 {
		return 0
	}
	num := amountCents * pointsPerYuan * bonusPM
	return (num + 50000) / 100000
}

// CO2SavedG 减碳量(克) = 重量(克) × 每千克减碳克数 / 1000
func CO2SavedG(weightG, co2GPerKg int64) int64 {
	return weightG * co2GPerKg / 1000
}

// HaversineKm 两点球面距离(公里)
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// ---- 回收员收益规则 ----
const (
	EarningBaseCents      = 300 // 每单基础 3 元
	EarningPerKgCents     = 20  // 每公斤 0.2 元
	EarningPerFloorCents  = 50  // 无电梯 3 层以上每层 0.5 元
	EarningPerKmCents     = 100 // 每公里 1 元
	EarningFloorThreshold = 3
)

// CollectorEarning 计算回收员上门收益构成
// floorBonus: 无电梯且楼层 > 3 时按层补贴; distanceBonus: 按上门距离补贴
func ComputeCollectorEarning(totalWeightG int64, floor int, hasElevator bool, distanceKm float64) (base, weight, floorB, distB, total int64) {
	base = EarningBaseCents
	weight = totalWeightG * EarningPerKgCents / 1000
	if !hasElevator && floor > EarningFloorThreshold {
		floorB = int64(floor-EarningFloorThreshold) * EarningPerFloorCents
	}
	distB = int64(distanceKm*EarningPerKmCents + 0.5)
	total = base + weight + floorB + distB
	return
}

// VarianceOverTolerance 复称差异是否超差: 相对偏差 > 5% 且绝对偏差 > 500g
func IsVarianceOverTolerance(onsiteG, stationG int64) bool {
	if onsiteG <= 0 {
		return stationG != 0
	}
	diff := stationG - onsiteG
	if diff < 0 {
		diff = -diff
	}
	return diff > 500 && diff*1000 > onsiteG*50
}
