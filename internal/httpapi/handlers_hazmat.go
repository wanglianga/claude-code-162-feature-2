package httpapi

import (
	"net/http"
	"strconv"

	"recycle/internal/domain"
	"recycle/internal/store"
	"recycle/internal/svc"
)

// ---- 不可回收物混入处理 ----

// disposalPointJSON 处理点序列化
func disposalPointJSON(p *domain.HazmatDisposalPoint) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "street": p.Street, "address": p.Address,
		"lat": p.Lat, "lng": p.Lng, "accepts": p.Accepts,
		"open_hours": p.OpenHours, "phone": p.Phone,
		"distance_km": roundKm(p.DistanceKm),
	}
}

func roundKm(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}

func disposalPointsJSON(ps []*domain.HazmatDisposalPoint) []map[string]any {
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, disposalPointJSON(p))
	}
	return out
}

func rejectedItemJSON(r *domain.RejectedItem) map[string]any {
	out := map[string]any{
		"id": r.ID, "order_id": r.OrderID, "collector_id": r.CollectorID,
		"category": r.Category, "item_name": r.ItemName, "hazmat_code": r.HazmatCode,
		"risk_level": r.RiskLevel, "risk_note": r.RiskNote,
		"refusal_basis": r.RefusalBasis, "refusal_text": r.RefusalText,
		"disposal_advice": r.DisposalAdvice, "resident_insisted": r.ResidentInsisted,
		"status": r.Status, "created_at": r.CreatedAt,
	}
	if r.NearestPointID != nil {
		out["nearest_point_id"] = *r.NearestPointID
	}
	if len(r.RefusalBases) > 0 {
		out["refusal_bases"] = r.RefusalBases
	}
	if len(r.NearestPoints) > 0 {
		out["nearest_points"] = disposalPointsJSON(r.NearestPoints)
	}
	return out
}

func stationHazmatJSON(r *domain.StationHazmatRecord) map[string]any {
	out := map[string]any{
		"id": r.ID, "order_id": r.OrderID, "station_id": r.StationID,
		"operator_id": r.OperatorID, "collector_id": r.CollectorID,
		"category": r.Category, "item_name": r.ItemName, "hazmat_code": r.HazmatCode,
		"risk_level": r.RiskLevel, "risk_note": r.RiskNote,
		"weight_kg":           domain.GramsToKg(r.WeightG),
		"handling_cost_yuan":  domain.CentsToYuan(r.HandlingCostCents),
		"deduct_target":       r.DeductTarget,
		"earning_deduct_yuan": domain.CentsToYuan(r.EarningDeductCents),
		"status":              r.Status, "created_at": r.CreatedAt,
	}
	if r.TrainingID != nil {
		out["training_id"] = *r.TrainingID
	}
	if r.InboundID != nil {
		out["inbound_id"] = *r.InboundID
	}
	return out
}

func stationHazmatListJSON(rs []*domain.StationHazmatRecord) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, stationHazmatJSON(r))
	}
	return out
}

func trainingJSON(t *domain.CollectorTrainingRecord) map[string]any {
	out := map[string]any{
		"id": t.ID, "collector_id": t.CollectorID, "kind": t.Kind,
		"reason": t.Reason, "deduct_yuan": domain.CentsToYuan(t.DeductCents),
		"status": t.Status, "created_at": t.CreatedAt,
	}
	if t.OrderID != nil {
		out["order_id"] = *t.OrderID
	}
	if t.HazmatRecordID != nil {
		out["hazmat_record_id"] = *t.HazmatRecordID
	}
	if t.CompletedAt != nil {
		out["completed_at"] = *t.CompletedAt
	}
	return out
}

func trainingListJSON(ts []*domain.CollectorTrainingRecord) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, trainingJSON(t))
	}
	return out
}

// handleRejectItem 回收员现场标记不可回收物
func (s *Server) handleRejectItem(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		Category         string `json:"category"`
		HazmatCode       string `json:"hazmat_code"`
		ItemName         string `json:"item_name"`
		RiskNote         string `json:"risk_note"`
		ResidentInsisted bool   `json:"resident_insisted"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	rec, guidance, err := s.svc.MarkHazmatRejected(r.Context(), u.ID, id, svc.RejectHazmatReq{
		Category:         req.Category,
		HazmatCode:       req.HazmatCode,
		ItemName:         req.ItemName,
		RiskNote:         req.RiskNote,
		ResidentInsisted: req.ResidentInsisted,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{
		"rejected_item": rejectedItemJSON(rec),
		"guidance": map[string]any{
			"hazmat_code":     guidance.HazmatCode,
			"item_name":       guidance.ItemName,
			"risk_level":      guidance.RiskLevel,
			"risk_note":       guidance.RiskNote,
			"refusal_bases":   guidance.RefusalBases,
			"disposal_advice": guidance.DisposalAdvice,
			"nearest_points":  disposalPointsJSON(guidance.NearestPoints),
		},
	})
}

// handleDisposalGuidance 居民端: 正确投放建议 + 附近处理点
func (s *Server) handleDisposalGuidance(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	q := r.URL.Query()
	hazmatCode := q.Get("hazmat_code")
	if hazmatCode == "" {
		writeErr(w, &svc.ApiError{Code: 400, Message: "hazmat_code required"})
		return
	}
	var orderID int64
	if v := q.Get("order_id"); v != "" {
		orderID, _ = strconv.ParseInt(v, 10, 64)
	}
	g, err := s.svc.DisposalGuidance(r.Context(), u.ID, orderID, hazmatCode)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"hazmat_code": g.HazmatCode, "item_name": g.ItemName,
		"risk_level": g.RiskLevel, "risk_note": g.RiskNote,
		"refusal_bases": g.RefusalBases, "disposal_advice": g.DisposalAdvice,
		"nearest_points": disposalPointsJSON(g.NearestPoints),
	})
}

// handleStationHazmat 站点入库标记误收不可回收物(处理成本+环保风险+异常扣款)
func (s *Server) handleStationHazmat(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	orderID, err := pathID(r, "orderId")
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		HazmatCode       string  `json:"hazmat_code"`
		Category         string  `json:"category"`
		ItemName         string  `json:"item_name"`
		WeightKg         float64 `json:"weight_kg"`
		HandlingCostYuan float64 `json:"handling_cost_yuan"`
		DeductTarget     string  `json:"deduct_target"` // ORDER | TRAINING
		RiskNote         string  `json:"risk_note"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	rec, err := s.svc.StationMarkHazmat(r.Context(), u.ID, orderID, svc.StationHazmatReq{
		HazmatCode:       req.HazmatCode,
		Category:         req.Category,
		ItemName:         req.ItemName,
		WeightKg:         req.WeightKg,
		HandlingCostYuan: req.HandlingCostYuan,
		DeductTarget:     req.DeductTarget,
		RiskNote:         req.RiskNote,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"hazmat_record": stationHazmatJSON(rec)})
}

// handleStationHazmatList 站点查看本环保风险记录
func (s *Server) handleStationHazmatList(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	st, err := store.StationByUserID(r.Context(), s.st.Pool, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	recs, err := store.ListStationHazmat(r.Context(), s.st.Pool, st.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"hazmat_records": stationHazmatListJSON(recs)})
}

// handleCollectorTraining 回收员查看本人培训/再教育记录
func (s *Server) handleCollectorTraining(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	recs, err := store.ListTrainingRecords(r.Context(), s.st.Pool, u.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	var totalDeduct int64
	for _, t := range recs {
		totalDeduct += t.DeductCents
	}
	writeJSON(w, 200, map[string]any{
		"training_records":  trainingListJSON(recs),
		"total_deduct_yuan": domain.CentsToYuan(totalDeduct),
	})
}

// handleCollectorHazmat 回收员查看本人误收异常
func (s *Server) handleCollectorHazmat(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	recs, err := store.ListStationHazmatByCollector(r.Context(), s.st.Pool, u.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"hazmat_records": stationHazmatListJSON(recs)})
}

// handleStreetHazmatRisks 街道查看辖区环保风险记录
func (s *Server) handleStreetHazmatRisks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT h.id, h.order_id, h.station_id, h.operator_id, h.collector_id, h.category,
		        h.item_name, h.hazmat_code, h.risk_level, h.risk_note, h.weight_g,
		        h.handling_cost_cents, h.deduct_target, h.earning_deduct_cents,
		        h.training_id, h.inbound_id, h.status, h.created_at, o.street
		 FROM station_hazmat_records h JOIN orders o ON o.id=h.order_id
		 WHERE ($1='' OR o.street=$1)
		 ORDER BY h.id DESC LIMIT 200`, r.URL.Query().Get("street"))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	var highCnt, totalCnt int
	var totalCost int64
	for rows.Next() {
		rec := &domain.StationHazmatRecord{}
		var stName string
		if err := rows.Scan(&rec.ID, &rec.OrderID, &rec.StationID, &rec.OperatorID, &rec.CollectorID,
			&rec.Category, &rec.ItemName, &rec.HazmatCode, &rec.RiskLevel, &rec.RiskNote, &rec.WeightG,
			&rec.HandlingCostCents, &rec.DeductTarget, &rec.EarningDeductCents, &rec.TrainingID,
			&rec.InboundID, &rec.Status, &rec.CreatedAt, &stName); err != nil {
			writeErr(w, err)
			return
		}
		item := stationHazmatJSON(rec)
		item["street"] = stName
		out = append(out, item)
		totalCnt++
		totalCost += rec.HandlingCostCents
		if rec.RiskLevel == domain.RiskHigh {
			highCnt++
		}
	}
	writeJSON(w, 200, map[string]any{
		"hazmat_records": out, "total": totalCnt, "high_risk": highCnt,
		"total_handling_cost_yuan": domain.CentsToYuan(totalCost),
	})
}
