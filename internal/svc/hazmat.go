package svc

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"recycle/internal/domain"
	"recycle/internal/store"
)

// RejectHazmatReq 回收员现场标记不可回收物
type RejectHazmatReq struct {
	Category         string // 被混入的可回收品类(如 metal)
	HazmatCode       string // 不可回收物编码(如 paint_bucket)
	ItemName         string // 可选: 物品名称覆盖
	RiskNote         string // 可选: 现场补充风险说明
	ResidentInsisted bool   // 居民是否坚持要求带走
}

// HazmatGuidance 返回给居民端的拒收依据 + 正确投放建议 + 附近处理点
type HazmatGuidance struct {
	HazmatCode     string                        `json:"hazmat_code"`
	ItemName       string                        `json:"item_name"`
	RiskLevel      string                        `json:"risk_level"`
	RiskNote       string                        `json:"risk_note"`
	RefusalBases   []map[string]string           `json:"refusal_bases"`
	DisposalAdvice string                        `json:"disposal_advice"`
	NearestPoints  []*domain.HazmatDisposalPoint `json:"nearest_points"`
}

// MarkHazmatRejected 回收员现场识别不可回收物(如油漆桶混在金属中):
// 标记风险并说明; 居民坚持要求带走时给出拒收依据, 同时提供正确投放建议与附近处理点。
func (s *Service) MarkHazmatRejected(ctx context.Context, collectorID, orderID int64,
	req RejectHazmatReq) (*domain.RejectedItem, *HazmatGuidance, error) {

	info, ok := domain.LookupHazmat(req.HazmatCode)
	if !ok {
		return nil, nil, badReq("unknown hazmat code %s", req.HazmatCode)
	}
	if req.Category == "" {
		return nil, nil, badReq("category required")
	}
	if ok, err := store.CategoryExists(ctx, s.Store.Pool, req.Category); err != nil {
		return nil, nil, err
	} else if !ok {
		return nil, nil, badReq("unknown category %s", req.Category)
	}

	var rec *domain.RejectedItem
	var guidance *HazmatGuidance
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		o, err := s.loadCollectorOrder(ctx, tx, collectorID, orderID)
		if err != nil {
			return err
		}
		// 现场核验前后、称重前均可标记
		if o.Status != domain.StatusArrived && o.Status != domain.StatusResidentVerified {
			return conflict("hazmat can only be marked on site before weighing (status %s)", o.Status)
		}
		// 同物品重复标记防护
		existing, err := store.ListRejectedItemsByOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.HazmatCode == req.HazmatCode && e.Category == req.Category {
				return conflict("hazmat %s already marked for this order", req.HazmatCode)
			}
		}

		itemName := req.ItemName
		if itemName == "" {
			itemName = info.Name
		}
		riskNote := req.RiskNote
		if riskNote == "" {
			riskNote = info.RiskNote
		}
		basisCode, basisText := info.PrimaryRefusal()
		status := domain.RejectionRejected
		if req.ResidentInsisted {
			// 居民坚持: 出示依据后仍拒收, 物品由居民带回按建议处理
			status = domain.RejectionTakenBack
		}
		rec = &domain.RejectedItem{
			OrderID: orderID, CollectorID: collectorID, Category: req.Category,
			ItemName: itemName, HazmatCode: info.Code, RiskLevel: info.RiskLevel,
			RiskNote: riskNote, RefusalBasis: basisCode, RefusalText: basisText,
			DisposalAdvice: info.Advice, ResidentInsisted: req.ResidentInsisted,
			Status: status,
		}
		if err := store.CreateRejectedItem(ctx, tx, rec); err != nil {
			return err
		}

		// 附近处理点
		points, err := s.nearestDisposalPoints(ctx, tx, o.Lat, o.Lng, o.Street, info.Code)
		if err != nil {
			return err
		}
		if len(points) > 0 {
			rec.NearestPointID = &points[0].ID
			if _, err := tx.Exec(ctx, `UPDATE rejected_items SET nearest_point_id=$2 WHERE id=$1`,
				rec.ID, points[0].ID); err != nil {
				return err
			}
		}
		guidance = &HazmatGuidance{
			HazmatCode: info.Code, ItemName: itemName, RiskLevel: info.RiskLevel,
			RiskNote: riskNote, RefusalBases: info.AllRefusalBases(),
			DisposalAdvice: info.Advice, NearestPoints: points,
		}

		// 事件留痕(不变状态) + 通知居民
		ev := &domain.OrderEvent{
			OrderID: orderID, FromStatus: o.Status, ToStatus: o.Status,
			ActorRole: domain.RoleCollector, ActorID: collectorID,
			Detail: map[string]any{
				"kind": "hazmat_rejected", "hazmat_code": info.Code, "item_name": itemName,
				"category": req.Category, "risk_level": info.RiskLevel,
				"resident_insisted": req.ResidentInsisted, "refusal_basis": basisCode,
			},
		}
		if err := store.AddOrderEvent(ctx, tx, ev); err != nil {
			return err
		}
		if err := store.AddOutbox(ctx, tx, domain.TopicOrderEvents, fmt.Sprintf("order:%d", orderID), map[string]any{
			"event_id": ev.ID, "order_id": orderID, "order_no": o.OrderNo,
			"from": o.Status, "to": o.Status, "actor_role": domain.RoleCollector,
			"actor_id": collectorID, "resident_id": o.ResidentID, "collector_id": o.CollectorID,
			"street": o.Street, "detail": ev.Detail, "at": time.Now().UTC(),
		}); err != nil {
			return err
		}
		notice := fmt.Sprintf("您混放在%s中的【%s】属不可回收物(%s风险), 回收员无法带走; %s",
			req.Category, itemName, riskLabel(info.RiskLevel), info.Advice)
		return s.notify(ctx, tx, o.ResidentID, "HAZMAT_REJECTED", notice)
	})
	if err != nil {
		return nil, nil, err
	}
	rec.RefusalBases, rec.NearestPoints = guidance.RefusalBases, guidance.NearestPoints
	return rec, guidance, nil
}

// DisposalGuidance 居民端查询正确投放建议与附近处理点
// orderID > 0 时按订单地址定位; 否则按居民档案地址
func (s *Service) DisposalGuidance(ctx context.Context, residentID, orderID int64,
	hazmatCode string) (*HazmatGuidance, error) {

	info, ok := domain.LookupHazmat(hazmatCode)
	if !ok {
		return nil, badReq("unknown hazmat code %s", hazmatCode)
	}
	var lat, lng float64
	var street string
	if orderID > 0 {
		o, err := store.OrderByID(ctx, s.Store.Pool, orderID)
		if err != nil {
			return nil, err
		}
		if o.ResidentID != residentID {
			return nil, forbidden("not your order")
		}
		lat, lng, street = o.Lat, o.Lng, o.Street
	} else {
		res, err := store.ResidentByID(ctx, s.Store.Pool, residentID)
		if err != nil {
			return nil, err
		}
		lat, lng, street = res.Lat, res.Lng, res.Street
	}
	points, err := s.nearestDisposalPoints(ctx, s.Store.Pool, lat, lng, street, info.Code)
	if err != nil {
		return nil, err
	}
	return &HazmatGuidance{
		HazmatCode: info.Code, ItemName: info.Name, RiskLevel: info.RiskLevel,
		RiskNote: info.RiskNote, RefusalBases: info.AllRefusalBases(),
		DisposalAdvice: info.Advice, NearestPoints: points,
	}, nil
}

// nearestDisposalPoints 查附近处理点: 优先同街道, 按直线距离升序; 接收目录含该危废编码
func (s *Service) nearestDisposalPoints(ctx context.Context, q store.Querier,
	lat, lng float64, street, hazmatCode string) ([]*domain.HazmatDisposalPoint, error) {

	points, err := store.ListHazmatPoints(ctx, q, street)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		// 本街道无处理点时返回全部
		points, err = store.ListHazmatPoints(ctx, q, "")
		if err != nil {
			return nil, err
		}
	}
	out := make([]*domain.HazmatDisposalPoint, 0, len(points))
	for _, p := range points {
		if len(p.Accepts) > 0 && !containsStr(p.Accepts, hazmatCode) {
			continue
		}
		p.DistanceKm = domain.HaversineKm(lat, lng, p.Lat, p.Lng)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistanceKm < out[j].DistanceKm })
	if len(out) > 5 {
		out = out[:5]
	}
	return out, nil
}

// StationHazmatReq 站点入库人员标记误收不可回收物
type StationHazmatReq struct {
	HazmatCode       string
	Category         string
	ItemName         string
	WeightKg         float64
	HandlingCostYuan float64 // 处理成本(元), 站点入库人员标记
	DeductTarget     string  // ORDER | TRAINING
	RiskNote         string
}

// StationMarkHazmat 站点入库发现回收员误收不可回收物:
// 生成环保风险记录 + 异常扣款(从对应订单收益或培训记录体现)。
func (s *Service) StationMarkHazmat(ctx context.Context, stationUserID, orderID int64,
	req StationHazmatReq) (*domain.StationHazmatRecord, error) {

	info, ok := domain.LookupHazmat(req.HazmatCode)
	if !ok {
		return nil, badReq("unknown hazmat code %s", req.HazmatCode)
	}
	if req.DeductTarget != domain.DeductFromOrder && req.DeductTarget != domain.DeductFromTraining {
		return nil, badReq("deduct_target must be ORDER or TRAINING")
	}
	if req.WeightKg < 0 || req.WeightKg > 10000 {
		return nil, badReq("invalid weight_kg")
	}
	costCents := domain.YuanToCents(req.HandlingCostYuan)
	if costCents < 0 {
		return nil, badReq("invalid handling_cost")
	}

	station, err := store.StationByUserID(ctx, s.Store.Pool, stationUserID)
	if err != nil {
		return nil, err
	}
	var rec *domain.StationHazmatRecord
	err = s.Store.InTx(ctx, func(tx pgx.Tx) error {
		o, err := store.OrderByIDForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.StationID == nil || *o.StationID != station.ID {
			return forbidden("order not delivered to your station")
		}
		// 入库环节: 送达后(复称前/后均可)
		if o.Status != domain.StatusDelivered && o.Status != domain.StatusInboundConfirmed &&
			o.Status != domain.StatusCompleted {
			return conflict("hazmat can only be registered at inbound (status %s)", o.Status)
		}
		if o.CollectorID == nil {
			return badReq("order has no collector")
		}
		collectorID := *o.CollectorID
		if n, err := store.CountStationHazmatForOrder(ctx, tx, orderID); err != nil {
			return err
		} else if n > 0 {
			// 一单仅登记一次误收异常, 防止重复扣款
			return conflict("hazmat record already exists for order %s", o.OrderNo)
		}

		itemName := req.ItemName
		if itemName == "" {
			itemName = info.Name
		}
		riskNote := req.RiskNote
		if riskNote == "" {
			riskNote = info.RiskNote
		}
		weightG := domain.KgToGrams(req.WeightKg)

		// 关联入库记录(复称后才有)
		var inboundID *int64
		if in, err := store.InboundByOrder(ctx, tx, orderID); err == nil {
			inboundID = &in.ID
		}

		rec = &domain.StationHazmatRecord{
			OrderID: orderID, StationID: station.ID, OperatorID: stationUserID,
			CollectorID: collectorID, Category: req.Category, ItemName: itemName,
			HazmatCode: info.Code, RiskLevel: info.RiskLevel, RiskNote: riskNote,
			WeightG: weightG, HandlingCostCents: costCents, DeductTarget: req.DeductTarget,
			InboundID: inboundID, Status: "CONFIRMED",
		}
		if err := store.CreateStationHazmat(ctx, tx, rec); err != nil {
			return err
		}

		// 异常扣款去向
		switch req.DeductTarget {
		case domain.DeductFromOrder:
			// 从对应订单结算产生的回收员收益中扣减(以当前收益为上限)
			e, err := store.EarningByOrder(ctx, tx, orderID)
			if err != nil {
				if err == store.ErrNotFound {
					return badReq("order has no earning record; deduct via TRAINING instead")
				}
				return err
			}
			deduct := costCents
			if deduct > e.TotalCents {
				deduct = e.TotalCents
			}
			if err := store.ApplyHazmatEarningDeduct(ctx, tx, orderID, deduct); err != nil {
				return err
			}
			rec.EarningDeductCents = deduct
			if _, err := tx.Exec(ctx,
				`UPDATE station_hazmat_records SET earning_deduct_cents=$2 WHERE id=$1`,
				rec.ID, deduct); err != nil {
				return err
			}
		case domain.DeductFromTraining:
			// 计入培训/再教育记录(绩效扣款在培训记录中体现)
			tr := &domain.CollectorTrainingRecord{
				CollectorID: collectorID, OrderID: &orderID, HazmatRecordID: &rec.ID,
				Kind: "HAZMAT_MISACCEPT",
				Reason: fmt.Sprintf("误收不可回收物【%s】(%s, %s风险)混入%s, 须参加危废识别再培训; 处理成本 %.2f 元计入绩效扣款",
					itemName, info.Code, riskLabel(info.RiskLevel), req.Category, req.HandlingCostYuan),
				DeductCents: costCents, Status: domain.TrainingOpen,
			}
			if err := store.CreateTrainingRecord(ctx, tx, tr); err != nil {
				return err
			}
			rec.TrainingID = &tr.ID
			if _, err := tx.Exec(ctx, `UPDATE station_hazmat_records SET training_id=$2 WHERE id=$1`,
				rec.ID, tr.ID); err != nil {
				return err
			}
		}

		// 事件留痕(不变状态) + 通知责任回收员
		ev := &domain.OrderEvent{
			OrderID: orderID, FromStatus: o.Status, ToStatus: o.Status,
			ActorRole: domain.RoleStation, ActorID: stationUserID,
			Detail: map[string]any{
				"kind": "hazmat_misaccepted", "hazmat_record_id": rec.ID,
				"hazmat_code": info.Code, "item_name": itemName, "risk_level": info.RiskLevel,
				"weight_g": weightG, "handling_cost_cents": costCents,
				"deduct_target":        req.DeductTarget,
				"earning_deduct_cents": rec.EarningDeductCents, "training_id": rec.TrainingID,
				"collector_id": collectorID,
			},
		}
		if err := store.AddOrderEvent(ctx, tx, ev); err != nil {
			return err
		}
		if err := store.AddOutbox(ctx, tx, domain.TopicStationEvents, fmt.Sprintf("order:%d", orderID), map[string]any{
			"event": "hazmat_misaccepted", "hazmat_record_id": rec.ID, "order_id": orderID,
			"station_id": station.ID, "collector_id": collectorID,
			"hazmat_code": info.Code, "risk_level": info.RiskLevel,
			"handling_cost_cents": costCents, "deduct_target": req.DeductTarget,
			"at": time.Now().UTC(),
		}); err != nil {
			return err
		}
		return s.notify(ctx, tx, collectorID, "HAZMAT_MISACCEPTED",
			fmt.Sprintf("订单 %s 入库发现您误收【%s】(不可回收物), 环保风险=%s, 处理成本 %.2f 元已按%s%s",
				o.OrderNo, itemName, riskLabel(info.RiskLevel), req.HandlingCostYuan,
				deductLabel(req.DeductTarget), deductSuffix(req.DeductTarget, rec.TrainingID)))
	})
	return rec, err
}

func riskLabel(level string) string {
	switch level {
	case domain.RiskHigh:
		return "高"
	case domain.RiskMedium:
		return "中"
	default:
		return "低"
	}
}

func deductLabel(target string) string {
	if target == domain.DeductFromTraining {
		return "培训记录体现"
	}
	return "订单收益扣减"
}

func deductSuffix(target string, trainingID *int64) string {
	if target == domain.DeductFromTraining && trainingID != nil {
		return fmt.Sprintf("(培训记录 #%d)", *trainingID)
	}
	return ""
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
