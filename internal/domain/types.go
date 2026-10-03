// Package domain 定义回收服务的核心领域类型、常量与单位换算。
// 内部统一使用整数: 金额=分(cents), 重量=克(grams), 比例=千分比(per-mille)。
package domain

import "time"

// 订单状态机状态
const (
	StatusPending          = "PENDING"           // 已预约, 待派单
	StatusAssigned         = "ASSIGNED"          // 已派单, 待回收员接单
	StatusAccepted         = "ACCEPTED"          // 回收员已接单
	StatusEnRoute          = "EN_ROUTE"          // 回收员出发
	StatusArrived          = "ARRIVED"           // 已到达
	StatusResidentVerified = "RESIDENT_VERIFIED" // 已核验居民身份
	StatusWeighed          = "WEIGHED"           // 现场称重定价完成, 待居民确认
	StatusDisputed         = "DISPUTED"          // 称重/价格争议中, 冻结
	StatusConfirmed        = "CONFIRMED"         // 居民确认称重与价格
	StatusSettled          = "SETTLED"           // 结算完成(现金或积分)
	StatusDelivered        = "DELIVERED"         // 已送达回收站, 待复称
	StatusInboundConfirmed = "INBOUND_CONFIRMED" // 站点复称完成
	StatusCompleted        = "COMPLETED"         // 环保凭证已开具, 流程结束
	StatusCancelled        = "CANCELLED"
)

// 角色
const (
	RoleResident  = "resident"
	RoleCollector = "collector"
	RoleStation   = "station"
	RoleStreet    = "street"
	RoleSystem    = "system"
)

// 品类
const (
	CatPaper     = "paper"     // 废纸
	CatPlastic   = "plastic"   // 塑料
	CatMetal     = "metal"     // 金属
	CatAppliance = "appliance" // 小家电
	CatClothing  = "clothing"  // 旧衣物
)

// 明细来源
const (
	SourceBooking     = "BOOKING"
	SourceOnsiteAdded = "ONSITE_ADDED"
)

// 确认方式
const (
	MethodScanCode = "SCAN_CODE" // 扫核验码
	MethodSelf     = "SELF"      // 居民本人 App 确认
	MethodAssisted = "ASSISTED"  // 回收员协助确认(老人等)
	MethodSystem   = "SYSTEM"
)

// 结算方式
const (
	SettleCash   = "cash"
	SettlePoints = "points"
)

// 结算状态
const (
	SettlePending = "PENDING"
	SettleSuccess = "SUCCESS"
	SettleFailed  = "FAILED"
)

// 复称差异
const (
	VarianceNone          = "NONE"
	VarianceOK            = "OK"
	VarianceOverTolerance = "OVER_TOLERANCE"
)

// Kafka topics
const (
	TopicOrderEvents    = "recycle.order.events"
	TopicPointsCommands = "recycle.points.commands"
	TopicPointsEvents   = "recycle.points.events"
	TopicStationEvents  = "recycle.station.events"
	TopicNotifications  = "recycle.notifications"
)

// ---- 单位换算 ----

// KgToGrams 千克 -> 克 (四舍五入到克)
func KgToGrams(kg float64) int64 {
	return int64(kg*1000 + 0.5)
}

// GramsToKg 克 -> 千克
func GramsToKg(g int64) float64 {
	return float64(g) / 1000.0
}

// YuanToCents 元 -> 分 (四舍五入到分)
func YuanToCents(yuan float64) int64 {
	if yuan >= 0 {
		return int64(yuan*100 + 0.5)
	}
	return int64(yuan*100 - 0.5)
}

// CentsToYuan 分 -> 元
func CentsToYuan(c int64) float64 {
	return float64(c) / 100.0
}

// ---- 实体 ----

type User struct {
	ID      int64
	Account string
	Name    string
	Role    string
	Token   string
	Phone   string
}

type Resident struct {
	UserID      int64
	Street      string
	Address     string
	Lat         float64
	Lng         float64
	Floor       int
	HasElevator bool
	IsElderly   bool
}

type Collector struct {
	UserID        int64
	StationID     int64
	OnDuty        bool
	Lat           float64
	Lng           float64
	MaxConcurrent int
}

type Station struct {
	ID      int64
	UserID  int64
	Name    string
	Street  string
	Lat     float64
	Lng     float64
}

type Order struct {
	ID              int64     `json:"id"`
	OrderNo         string    `json:"order_no"`
	ResidentID      int64     `json:"resident_id"`
	CollectorID     *int64    `json:"collector_id,omitempty"`
	StationID       *int64    `json:"station_id,omitempty"`
	Status          string    `json:"status"`
	Street          string    `json:"street"`
	Address         string    `json:"address"`
	Lat             float64   `json:"lat"`
	Lng             float64   `json:"lng"`
	Floor           int       `json:"floor"`
	HasElevator     bool      `json:"has_elevator"`
	TimeWindowStart time.Time `json:"time_window_start"`
	TimeWindowEnd   time.Time `json:"time_window_end"`
	Photos          []string  `json:"photos"`
	VerifyCode      string    `json:"-"`
	EstimateCents   int64     `json:"estimate_cents"`
	OnsiteCents     int64     `json:"onsite_cents"`
	FinalCents      int64     `json:"final_cents"`
	SettlementType  *string   `json:"settlement_type,omitempty"`
	LateMinutes     int       `json:"late_minutes"`
	CancelReason    *string   `json:"cancel_reason,omitempty"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type OrderItem struct {
	ID               int64  `json:"id"`
	OrderID          int64  `json:"order_id"`
	Category         string `json:"category"`
	Source           string `json:"source"`
	EstWeightG       int64  `json:"est_weight_g"`
	EstPriceCents    int64  `json:"est_price_cents"`
	EstCents         int64  `json:"est_cents"`
	OnsiteWeightG    *int64 `json:"onsite_weight_g,omitempty"`
	ContaminationPM  int    `json:"contamination_pm"`
	OnsitePriceCents *int64 `json:"onsite_price_cents,omitempty"`
	OnsiteCents      *int64 `json:"onsite_cents,omitempty"`
	StationWeightG   *int64 `json:"station_weight_g,omitempty"`
	StationCents     *int64 `json:"station_cents,omitempty"`
	VarianceG        *int64 `json:"variance_g,omitempty"`
	VarianceStatus   string `json:"variance_status"`
}

type OrderEvent struct {
	ID         int64          `json:"id"`
	OrderID    int64          `json:"order_id"`
	FromStatus string         `json:"from_status"`
	ToStatus   string         `json:"to_status"`
	ActorRole  string         `json:"actor_role"`
	ActorID    int64          `json:"actor_id"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Confirmation struct {
	ID        int64          `json:"id"`
	OrderID   int64          `json:"order_id"`
	Kind      string         `json:"kind"`
	ActorRole string         `json:"actor_role"`
	ActorID   int64          `json:"actor_id"`
	Method    string         `json:"method"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type Settlement struct {
	ID             int64     `json:"id"`
	OrderID        int64     `json:"order_id"`
	Type           string    `json:"type"`
	AmountCents    int64     `json:"amount_cents"`
	Points         int64     `json:"points"`
	Status         string    `json:"status"`
	FailReason     *string   `json:"fail_reason,omitempty"`
	IdempotencyKey string    `json:"-"`
	RetryCount     int       `json:"retry_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PointsEntry struct {
	ID             int64     `json:"id"`
	ResidentID     int64     `json:"resident_id"`
	OrderID        *int64    `json:"order_id,omitempty"`
	SettlementID   *int64    `json:"settlement_id,omitempty"`
	Delta          int64     `json:"delta"`
	BalanceAfter   int64     `json:"balance_after"`
	Kind           string    `json:"kind"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type Certificate struct {
	ID        int64     `json:"id"`
	CertNo    string    `json:"cert_no"`
	OrderID   int64     `json:"order_id"`
	ResidentID int64    `json:"resident_id"`
	Category  string    `json:"category"`
	WeightG   int64     `json:"weight_g"`
	CO2SavedG int64     `json:"co2_saved_g"`
	IssuedAt  time.Time `json:"issued_at"`
}

type InboundRecord struct {
	ID         int64     `json:"id"`
	OrderID    int64     `json:"order_id"`
	StationID  int64     `json:"station_id"`
	OperatorID int64     `json:"operator_id"`
	Status     string    `json:"status"`
	VarianceG  int64     `json:"variance_g"`
	CreatedAt  time.Time `json:"created_at"`
}

type Dispute struct {
	ID         int64          `json:"id"`
	OrderID    int64          `json:"order_id"`
	ItemID     *int64         `json:"item_id,omitempty"`
	RaisedBy   string         `json:"raised_by"`
	Kind       string         `json:"kind"`
	Detail     string         `json:"detail"`
	Status     string         `json:"status"`
	Resolution map[string]any `json:"resolution,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty"`
}

type Complaint struct {
	ID         int64      `json:"id"`
	OrderID    int64      `json:"order_id"`
	FromRole   string     `json:"from_role"`
	FromID     int64      `json:"from_id"`
	Content    string     `json:"content"`
	Status     string     `json:"status"`
	Resolution *string    `json:"resolution,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type CollectorEarning struct {
	ID                int64     `json:"id"`
	CollectorID       int64     `json:"collector_id"`
	OrderID           int64     `json:"order_id"`
	BaseCents         int64     `json:"base_cents"`
	WeightCents       int64     `json:"weight_cents"`
	FloorCents        int64     `json:"floor_cents"`
	DistanceCents     int64     `json:"distance_cents"`
	PenaltyCents      int64     `json:"penalty_cents"`
	HazmatDeductCents int64     `json:"hazmat_deduct_cents"` // 不可回收物误收异常扣款
	TotalCents        int64     `json:"total_cents"`
	CreatedAt         time.Time `json:"created_at"`
}

// ---- 不可回收物(危险废物)混入处理 ----

// HazmatDisposalPoint 附近危废/不可回收物处理点
type HazmatDisposalPoint struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Street     string    `json:"street"`
	Address    string    `json:"address"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	Accepts    []string  `json:"accepts"`
	OpenHours  string    `json:"open_hours"`
	Phone      string    `json:"phone"`
	DistanceKm float64   `json:"distance_km,omitempty"` // 查询时按距离排序填充
	CreatedAt  time.Time `json:"created_at"`
}

// RejectedItem 现场拒收记录(回收员标记不可回收物)
type RejectedItem struct {
	ID               int64                  `json:"id"`
	OrderID          int64                  `json:"order_id"`
	CollectorID      int64                  `json:"collector_id"`
	Category         string                 `json:"category"`
	ItemName         string                 `json:"item_name"`
	HazmatCode       string                 `json:"hazmat_code"`
	RiskLevel        string                 `json:"risk_level"`
	RiskNote         string                 `json:"risk_note"`
	RefusalBasis     string                 `json:"refusal_basis"`
	RefusalText      string                 `json:"refusal_text"`
	DisposalAdvice   string                 `json:"disposal_advice"`
	ResidentInsisted bool                   `json:"resident_insisted"`
	Status           string                 `json:"status"`
	NearestPointID   *int64                 `json:"nearest_point_id,omitempty"`
	RefusalBases     []map[string]string    `json:"refusal_bases,omitempty"` // 居民坚持时返回的全部依据
	NearestPoints    []*HazmatDisposalPoint `json:"nearest_points,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
}

// StationHazmatRecord 站点入库发现误收的异常记录(处理成本+环保风险+异常扣款)
type StationHazmatRecord struct {
	ID                 int64     `json:"id"`
	OrderID            int64     `json:"order_id"`
	StationID          int64     `json:"station_id"`
	OperatorID         int64     `json:"operator_id"`
	CollectorID        int64     `json:"collector_id"`
	Category           string    `json:"category"`
	ItemName           string    `json:"item_name"`
	HazmatCode         string    `json:"hazmat_code"`
	RiskLevel          string    `json:"risk_level"`
	RiskNote           string    `json:"risk_note"`
	WeightG            int64     `json:"weight_g"`
	HandlingCostCents  int64     `json:"handling_cost_cents"`
	DeductTarget       string    `json:"deduct_target"`
	EarningDeductCents int64     `json:"earning_deduct_cents"`
	TrainingID         *int64    `json:"training_id,omitempty"`
	InboundID          *int64    `json:"inbound_id,omitempty"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
}

// CollectorTrainingRecord 回收员培训/再教育记录(误收扣款体现)
type CollectorTrainingRecord struct {
	ID             int64      `json:"id"`
	CollectorID    int64      `json:"collector_id"`
	OrderID        *int64     `json:"order_id,omitempty"`
	HazmatRecordID *int64     `json:"hazmat_record_id,omitempty"`
	Kind           string     `json:"kind"`
	Reason         string     `json:"reason"`
	DeductCents    int64      `json:"deduct_cents"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}
