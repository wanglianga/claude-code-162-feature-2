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

// 不可回收物混入处理服务:
//  1. 现场拒收: 回收员标记(MarkOnsiteContaminant) -> 居民坚持要依据(RejectionBasis/Insist)
//     -> 回收员拒收闭环(RejectOnsiteContaminant) -> 居民端投放建议与附近处理点(DisposalGuidance)
//  2. 误收入库: 站点复称发现(StationReweigh 内 InboundContaminants) -> 环保风险记录 +
//     处理成本分摊(订单收益优先, 不足挂培训记录)。

// MarkContaminantReq 现场标记不可回收物
type MarkContaminantReq struct {
	ItemCategory string  // 混入的可回收品类(如 metal)
	ItemName     string  // 不可回收物名称(如 油漆桶)
	Code         string  // rejection_catalog 编码
	WeightKg     float64 // 估算重量
	RiskNote     string  // 回收员现场说明的风险补充
}

// loadCatalog 按编码取拒收目录, 缺省回落为"其他不可回收物"
func (s *Service) loadCatalog(ctx context.Context, q pgx.Tx, code string) (*domain.RejectionCatalogItem, error) {
	if code == "" {
		code = domain.ContamOtherNonRecyclable
	}
	c, err := store.RejectionCatalogByCode(ctx, q, code)
	if err == store.ErrNotFound {
		return nil, badReq("unknown contaminant code %s", code)
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// MarkOnsiteContaminant 回收员现场标记不可回收物并说明风险(不变更订单状态, 仅留痕)
func (s *Service) MarkOnsiteContaminant(ctx context.Context, collectorID, orderID int64,
	req MarkContaminantReq) (*domain.ContaminationRecord, error) {

	if req.ItemName == "" {
		return nil, badReq("item_name required")
	}
	if req.WeightKg < 0 || req.WeightKg > 1000 {
		return nil, badReq("invalid weight_kg")
	}
	var rec *domain.ContaminationRecord
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		o, err := s.loadCollectorOrder(ctx, tx, collectorID, orderID)
		if err != nil {
			return err
		}
		switch o.Status {
		case domain.StatusArrived, domain.StatusResidentVerified, domain.StatusWeighed:
		default:
			return conflict("contaminant can only be marked at onsite stage (status %s)", o.Status)
		}
		catalog, err := s.loadCatalog(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		// 关联同品类订单明细(如有)
		var itemID *int64
		if req.ItemCategory != "" {
			items, err := store.ListOrderItems(ctx, tx, orderID)
			if err != nil {
				return err
			}
			for _, it := range items {
				if it.Category == req.ItemCategory {
					id := it.ID
					itemID = &id
					break
				}
			}
		}
		riskDesc := catalog.RiskDesc
		if req.RiskNote != "" {
			riskDesc = catalog.RiskDesc + "；现场说明: " + req.RiskNote
		}
		rec = &domain.ContaminationRecord{
			OrderID: orderID, ItemID: itemID, ItemCategory: req.ItemCategory,
			ItemName: req.ItemName, ContaminantCode: catalog.Code,
			DetectedStage: domain.StageOnsite, Status: domain.ContamMarked,
			WeightG: domain.KgToGrams(req.WeightKg), RiskLevel: catalog.RiskLevel,
			RiskDesc: riskDesc, RejectionBasis: catalog.Basis, DisposalAdvice: catalog.Advice,
			DetectorID: collectorID, DetectorRole: domain.RoleCollector,
		}
		if err := store.CreateContaminationRecord(ctx, tx, rec); err != nil {
			return err
		}
		ev := &domain.OrderEvent{
			OrderID: orderID, FromStatus: o.Status, ToStatus: o.Status,
			ActorRole: domain.RoleCollector, ActorID: collectorID,
			Detail: map[string]any{
				"kind": "contamination_marked", "contamination_id": rec.ID,
				"item_name": req.ItemName, "item_category": req.ItemCategory,
				"code": catalog.Code, "risk_level": catalog.RiskLevel,
				"detected_stage": domain.StageOnsite,
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
		return s.notify(ctx, tx, o.ResidentID, "CONTAMINATION_MARKED",
			fmt.Sprintf("回收员现场标记「%s」为不可回收物(%s风险): %s", req.ItemName, catalog.RiskLevel, catalog.RiskDesc))
	})
	return rec, err
}

// loadOwnedContam 读取混入记录并校验当前居民归属
func (s *Service) loadOwnedContam(ctx context.Context, tx pgx.Tx, residentID, recID int64) (*domain.ContaminationRecord, *domain.Order, error) {
	rec, err := store.ContaminationRecordByIDForUpdate(ctx, tx, recID)
	if err != nil {
		return nil, nil, err
	}
	o, err := store.OrderByID(ctx, tx, rec.OrderID)
	if err != nil {
		return nil, nil, err
	}
	if o.ResidentID != residentID {
		return nil, nil, forbidden("not your contamination record")
	}
	return rec, o, nil
}

// InsistRejection 居民坚持要求回收员带走: 服务登记坚持并返回拒收依据
func (s *Service) InsistRejection(ctx context.Context, residentID, recID int64) (*domain.ContaminationRecord, error) {
	var rec *domain.ContaminationRecord
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		rec, _, err = s.loadOwnedContam(ctx, tx, residentID, recID)
		if err != nil {
			return err
		}
		if rec.Status != domain.ContamMarked {
			return conflict("contamination record %d already %s", recID, rec.Status)
		}
		if err := store.MarkContamResidentInsists(ctx, tx, recID); err != nil {
			return err
		}
		rec.ResidentInsists = true
		ev := &domain.OrderEvent{
			OrderID: rec.OrderID, FromStatus: "", ToStatus: "",
			ActorRole: domain.RoleResident, ActorID: residentID,
			Detail: map[string]any{"kind": "contamination_resident_insists", "contamination_id": recID},
		}
		return store.AddOrderEvent(ctx, tx, ev)
	})
	return rec, err
}

// RejectOnsiteContaminant 回收员拒收闭环: MARKED -> REJECTED, 通知居民正确投放方式
func (s *Service) RejectOnsiteContaminant(ctx context.Context, collectorID, recID int64, note string) (*domain.ContaminationRecord, error) {
	var rec *domain.ContaminationRecord
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		rec0, err := store.ContaminationRecordByIDForUpdate(ctx, tx, recID)
		if err != nil {
			return err
		}
		o, err := s.loadCollectorOrder(ctx, tx, collectorID, rec0.OrderID)
		if err != nil {
			return err
		}
		if rec0.DetectedStage != domain.StageOnsite || rec0.Status != domain.ContamMarked {
			return conflict("contamination record %d is not an onsite MARKED record", recID)
		}
		if err := store.ResolveContamRejected(ctx, tx, recID); err != nil {
			return err
		}
		rec0.Status = domain.ContamRejected
		rec0.ResidentInsists = true
		ev := &domain.OrderEvent{
			OrderID: o.ID, FromStatus: o.Status, ToStatus: o.Status,
			ActorRole: domain.RoleCollector, ActorID: collectorID,
			Detail: map[string]any{
				"kind": "contamination_rejected", "contamination_id": recID,
				"item_name": rec0.ItemName, "basis": rec0.RejectionBasis,
				"resident_insists": rec0.ResidentInsists, "note": note,
			},
		}
		if err := store.AddOrderEvent(ctx, tx, ev); err != nil {
			return err
		}
		if err := store.AddOutbox(ctx, tx, domain.TopicOrderEvents, fmt.Sprintf("order:%d", o.ID), map[string]any{
			"event_id": ev.ID, "order_id": o.ID, "order_no": o.OrderNo,
			"from": o.Status, "to": o.Status, "actor_role": domain.RoleCollector,
			"actor_id": collectorID, "resident_id": o.ResidentID, "collector_id": o.CollectorID,
			"street": o.Street, "detail": ev.Detail, "at": time.Now().UTC(),
		}); err != nil {
			return err
		}
		if err := s.notify(ctx, tx, o.ResidentID, "CONTAMINATION_REJECTED",
			fmt.Sprintf("「%s」依规拒收: %s。%s", rec0.ItemName, rec0.RejectionBasis, rec0.DisposalAdvice)); err != nil {
			return err
		}
		rec = rec0
		return nil
	})
	return rec, err
}

// NearbyPoint 附近处理点(带距离)
type NearbyPoint struct {
	Point      *domain.DisposalPoint
	DistanceKm float64
}

// nearestDisposalPoints 按经纬度返回附近的规范处理点(可按接收类型过滤)
func (s *Service) nearestDisposalPoints(ctx context.Context, tx pgx.Tx,
	lat, lng float64, acceptType string, limit int) ([]NearbyPoint, error) {

	all, err := store.ListDisposalPoints(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []NearbyPoint{}
	for _, p := range all {
		if acceptType != "" {
			matched := false
			for _, a := range p.Accepts {
				if a == acceptType {
					matched = true
					break
				}
			}

			if !matched {
				continue
			}
		}
		d := domain.HaversineKm(lat, lng, p.Lat, p.Lng)
		out = append(out, NearbyPoint{Point: p, DistanceKm: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistanceKm < out[j].DistanceKm })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DisposalGuidance 拒收后居民端: 拒收记录 + 正确投放建议 + 附近处理点
func (s *Service) DisposalGuidance(ctx context.Context, residentID, recID int64) (map[string]any, error) {
	var out map[string]any
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		rec, o, err := s.loadOwnedContam(ctx, tx, residentID, recID)
		if err != nil {
			return err
		}
		catalog, err := store.RejectionCatalogByCode(ctx, tx, rec.ContaminantCode)
		if err != nil {
			return err
		}
		nearby, err := s.nearestDisposalPoints(ctx, tx, o.Lat, o.Lng, catalog.DisposalType, 5)
		if err != nil {
			return err
		}
		points := []map[string]any{}
		for _, n := range nearby {
			points = append(points, map[string]any{
				"id": n.Point.ID, "name": n.Point.Name, "address": n.Point.Address,
				"street": n.Point.Street, "open_hours": n.Point.OpenHours, "phone": n.Point.Phone,
				"accepts": n.Point.Accepts, "lat": n.Point.Lat, "lng": n.Point.Lng,
				"distance_km": float64(int(n.DistanceKm*100+0.5)) / 100,
			})
		}
		out = map[string]any{
			"contamination":   rec,
			"catalog":         catalog,
			"advice":          catalog.Advice,
			"rejection_basis": catalog.Basis,
			"nearby_points":   points,
		}
		return nil
	})
	return out, err
}

// RejectionGuide 公共: 按不可回收物编码查拒收依据与投放建议(可带经纬度查附近处理点)
func (s *Service) RejectionGuide(ctx context.Context, code string, lat, lng float64) (map[string]any, error) {
	if code == "" {
		code = domain.ContamOtherNonRecyclable
	}
	out := map[string]any{}
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		catalog, err := store.RejectionCatalogByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		out["catalog"] = catalog
		if lat != 0 || lng != 0 {
			nearby, err := s.nearestDisposalPoints(ctx, tx, lat, lng, catalog.DisposalType, 5)
			if err != nil {
				return err
			}
			points := []map[string]any{}
			for _, n := range nearby {
				points = append(points, map[string]any{
					"id": n.Point.ID, "name": n.Point.Name, "address": n.Point.Address,
					"open_hours": n.Point.OpenHours, "phone": n.Point.Phone,
					"accepts":     n.Point.Accepts,
					"distance_km": float64(int(n.DistanceKm*100+0.5)) / 100,
				})
			}
			out["nearby_points"] = points
		}
		return nil
	})
	return out, err
}

// CompleteTraining 回收员完成培训(误收纠偏)
func (s *Service) CompleteTraining(ctx context.Context, collectorID, trainingID int64) (*domain.CollectorTrainingRecord, error) {
	var t *domain.CollectorTrainingRecord
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		list, err := store.ListTrainingRecords(ctx, tx, collectorID, 200)
		if err != nil {
			return err
		}
		found := false
		for _, r := range list {
			if r.ID == trainingID {
				t = r
				found = true
				break
			}
		}
		if !found {
			return notFound("training record")
		}
		return store.CompleteTrainingRecord(ctx, tx, trainingID)
	})
	if err == nil {
		t.Status = domain.TrainingCompleted
		now := time.Now()
		t.CompletedAt = &now
	}
	return t, err
}
