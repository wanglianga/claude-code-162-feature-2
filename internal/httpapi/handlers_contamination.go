package httpapi

import (
	"net/http"
	"strconv"

	"recycle/internal/domain"
	"recycle/internal/store"
	"recycle/internal/svc"
)

// ---- 回收员: 现场标记不可回收物 ----

func (s *Server) handleMarkContaminant(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		ItemCategory string  `json:"item_category"`
		ItemName     string  `json:"item_name"`
		Code         string  `json:"code"`
		WeightKg     float64 `json:"weight_kg"`
		RiskNote     string  `json:"risk_note"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	rec, err := s.svc.MarkOnsiteContaminant(r.Context(), u.ID, id, svc.MarkContaminantReq{
		ItemCategory: req.ItemCategory, ItemName: req.ItemName, Code: req.Code,
		WeightKg: req.WeightKg, RiskNote: req.RiskNote,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"contamination": contamJSON(rec)})
}

// ---- 回收员: 拒收闭环 ----

func (s *Server) handleRejectContaminant(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	cid, err := pathID(r, "cid")
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = decode(r, &req)
	rec, err := s.svc.RejectOnsiteContaminant(r.Context(), u.ID, cid, req.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"contamination": contamJSON(rec)})
}

// ---- 居民: 坚持要求带走(获取拒收依据) ----

func (s *Server) handleInsistContaminant(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	cid, err := pathID(r, "cid")
	if err != nil {
		writeErr(w, err)
		return
	}
	rec, err := s.svc.InsistRejection(r.Context(), u.ID, cid)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 坚持即返回拒收依据, 服务明确不予带走
	writeJSON(w, 200, map[string]any{
		"contamination":   contamJSON(rec),
		"rejection_basis": rec.RejectionBasis,
		"risk_level":      rec.RiskLevel,
		"risk_desc":       rec.RiskDesc,
		"decision":        "REJECTED_PER_REGULATION",
	})
}

// ---- 居民: 正确投放建议 + 附近处理点 ----

func (s *Server) handleContamGuidance(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	cid, err := pathID(r, "cid")
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.svc.DisposalGuidance(r.Context(), u.ID, cid)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// ---- 公共: 拒收目录/拒收依据查询 ----

func (s *Server) handleRejectionCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := store.ListRejectionCatalog(r.Context(), s.st.Pool)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"catalog": list})
}

func (s *Server) handleRejectionGuide(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	lat := parseFloatDefault(r.URL.Query().Get("lat"))
	lng := parseFloatDefault(r.URL.Query().Get("lng"))
	out, err := s.svc.RejectionGuide(r.Context(), code, lat, lng)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// ---- 站点: 环保风险记录 ----

func (s *Server) handleStationEnvRisks(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	st, err := store.StationByUserID(r.Context(), s.st.Pool, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	list, err := store.ListEnvRisks(r.Context(), s.st.Pool, st.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	var costTotal int64
	for _, rec := range list {
		out = append(out, contamJSON(rec))
		costTotal += rec.HandlingCostCents
	}
	writeJSON(w, 200, map[string]any{
		"station": st.Name, "env_risks": out,
		"handling_cost_total_yuan": domain.CentsToYuan(costTotal),
	})
}

// ---- 回收员: 培训记录 ----

func (s *Server) handleTrainingList(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	list, err := store.ListTrainingRecords(r.Context(), s.st.Pool, u.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	var charged int64
	for _, t := range list {
		out = append(out, trainingJSON(t))
		charged += t.ChargedCents
	}
	writeJSON(w, 200, map[string]any{
		"training":           out,
		"charged_total_yuan": domain.CentsToYuan(charged),
	})
}

func (s *Server) handleTrainingComplete(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	tid, err := pathID(r, "tid")
	if err != nil {
		writeErr(w, err)
		return
	}
	t, err := s.svc.CompleteTraining(r.Context(), u.ID, tid)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"training": trainingJSON(t)})
}

// ---- 序列化 ----

func contamJSON(r *domain.ContaminationRecord) map[string]any {
	out := map[string]any{
		"id": r.ID, "order_id": r.OrderID, "item_category": r.ItemCategory,
		"item_name": r.ItemName, "contaminant_code": r.ContaminantCode,
		"detected_stage": r.DetectedStage, "status": r.Status,
		"weight_kg": domain.GramsToKg(r.WeightG), "risk_level": r.RiskLevel,
		"risk_desc": r.RiskDesc, "rejection_basis": r.RejectionBasis,
		"disposal_advice": r.DisposalAdvice, "resident_insists": r.ResidentInsists,
		"handling_cost_yuan":    domain.CentsToYuan(r.HandlingCostCents),
		"order_charged_yuan":    domain.CentsToYuan(r.OrderChargedCents),
		"training_charged_yuan": domain.CentsToYuan(r.TrainingChargedCents),
		"charged_to":            r.ChargedTo, "detector_role": r.DetectorRole,
		"created_at": r.CreatedAt,
	}
	if r.ItemID != nil {
		out["item_id"] = *r.ItemID
	}
	if r.TrainingID != nil {
		out["training_id"] = *r.TrainingID
	}
	if r.StationID != nil {
		out["station_id"] = *r.StationID
	}
	if r.ResolvedAt != nil {
		out["resolved_at"] = *r.ResolvedAt
	}
	return out
}

func trainingJSON(t *domain.CollectorTrainingRecord) map[string]any {
	out := map[string]any{
		"id": t.ID, "collector_id": t.CollectorID, "kind": t.Kind,
		"title": t.Title, "content": t.Content, "status": t.Status,
		"cost_yuan":     domain.CentsToYuan(t.CostCents),
		"charged_yuan":  domain.CentsToYuan(t.ChargedCents),
		"charge_target": t.ChargeTarget, "created_at": t.CreatedAt,
	}
	if t.OrderID != nil {
		out["order_id"] = *t.OrderID
	}
	if t.CompletedAt != nil {
		out["completed_at"] = *t.CompletedAt
	}
	return out
}

func parseFloatDefault(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
