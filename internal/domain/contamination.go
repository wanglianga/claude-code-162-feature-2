package domain

// 不可回收物混入处理相关常量与纯规则。
//
// 两条链路:
//   - 现场拒收(ONSITE): 回收员上门发现居民把不可回收物(如油漆桶)混在可回收物中,
//     现场标记并说明风险; 居民坚持要求带走时服务给出拒收依据; 居民确认后拒收,
//     居民端得到正确投放建议与附近规范处理点。
//   - 误收入库(INBOUND): 回收员未识别而误收, 站点入库复称时发现, 入库人员标记处理成本,
//     生成环保风险记录; 结算时处理成本优先从该订单回收员收益中扣除, 不足部分挂
//     回收员培训记录(后续培训/收益中体现)。

// 发现环节
const (
	StageOnsite  = "ONSITE"  // 上门现场
	StageInbound = "INBOUND" // 站点入库
)

// 混入记录状态
const (
	ContamMarked            = "MARKED"             // 现场已标记, 待居民处置选择
	ContamRejected          = "REJECTED"           // 居民接受/坚持后仍拒收
	ContamErroneousAccepted = "ERRONEOUS_ACCEPTED" // 回收员误收, 站点入库发现
)

// 风险等级
const (
	RiskLow    = "LOW"
	RiskMedium = "MEDIUM"
	RiskHigh   = "HIGH"
)

// 处理成本承担/扣款去向
const (
	ChargeOrder    = "ORDER"    // 从对应订单收益扣除
	ChargeTraining = "TRAINING" // 挂培训记录
)

// 培训记录
const (
	TrainingKindMisaccept = "MISACCEPT"
	TrainingAssigned      = "ASSIGNED"
	TrainingCompleted     = "COMPLETED"
)

// 拒收目录编码
const (
	ContamPaintBucket        = "paint_bucket"
	ContamOtherNonRecyclable = "other_non_recyclable"
)

// ---- 纯规则 ----

// AllocateHandlingCost 处理成本在"订单收益"与"培训挂账"之间分摊:
// 优先扣订单收益, 不足部分挂培训记录。返回(订单扣款, 培训扣款)。
func AllocateHandlingCost(costCents, earningTotalCents int64) (orderPart, trainingPart int64) {
	if costCents <= 0 {
		return 0, 0
	}
	if earningTotalCents < 0 {
		earningTotalCents = 0
	}
	if costCents <= earningTotalCents {
		return costCents, 0
	}
	return earningTotalCents, costCents - earningTotalCents
}
