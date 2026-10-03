package svc

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"recycle/internal/domain"
	"recycle/internal/store"
)

// ReweighItem 站点复称行
type ReweighItem struct {
	Category string
	WeightKg float64
}

// InboundContaminant 入库时发现的误收不可回收物(回收员现场未识别)
type InboundContaminant struct {
	ItemCategory     string  // 混入的可回收品类, 如 metal
	ItemName         string  // 不可回收物名称, 如 油漆桶
	Code             string  // rejection_catalog 编码
	WeightKg         float64 // 估算重量
	HandlingCostYuan float64 // 入库人员标记的处理成本(元): 危废暂存/分拣/转运
	RiskNote         string  // 入库备注
}

// StationReweigh 站点二次称重:
//   - 逐品类复称, 与现场称重比对, 超差标记 OVER_TOLERANCE
//   - 生成入库记录, 重算订单最终金额
//   - 超差时: 扣减回收员收益; 积分结算单生成积分差额调整(可追)
//   - 误收不可回收物: 生成环保风险记录, 入库人员标记处理成本,
//     结算时优先从该订单收益扣除, 不足部分挂回收员培训记录
//   - 按品类开具环保凭证, 订单完结
func (s *Service) StationReweigh(ctx context.Context, stationUserID, orderID int64,
	weights []ReweighItem, contaminants []InboundContaminant) (*domain.Order, []*domain.OrderItem, []*domain.ContaminationRecord, error) {

	station, err := store.StationByUserID(ctx, s.Store.Pool, stationUserID)
	if err != nil {
		return nil, nil, nil, err
	}
	var o *domain.Order
	var items []*domain.OrderItem
	var riskRecords []*domain.ContaminationRecord
	err = s.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		o, err = store.OrderByIDForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.Status != domain.StatusDelivered {
			return conflict("order %s in status %s, expect DELIVERED", o.OrderNo, o.Status)
		}
		if o.StationID == nil || *o.StationID != station.ID {
			return forbidden("order not delivered to your station")
		}
		items, err = store.ListOrderItems(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		byCat := map[string]*domain.OrderItem{}
		for _, it := range items {
			byCat[it.Category] = it
		}
		seen := map[string]bool{}
		var finalTotal, totalVariance int64
		overTolerance := false
		varianceDetails := []map[string]any{}

		for _, w := range weights {
			it, ok := byCat[w.Category]
			if !ok {
				return badReq("category %s not in order", w.Category)
			}
			if w.WeightKg < 0 || w.WeightKg > 10000 {
				return badReq("invalid weight for %s", w.Category)
			}
			seen[w.Category] = true
			stationG := domain.KgToGrams(w.WeightKg)
			onsiteG := int64(0)
			if it.OnsiteWeightG != nil {
				onsiteG = *it.OnsiteWeightG
			}
			// 复称金额沿用现场扣杂率与锁定单价
			effG := domain.EffectiveWeightG(stationG, it.ContaminationPM)
			price := it.EstPriceCents
			if it.OnsitePriceCents != nil {
				price = *it.OnsitePriceCents
			}
			stationCents := domain.AmountForWeight(effG, price)
			variance := stationG - onsiteG
			vStatus := domain.VarianceOK
			if domain.IsVarianceOverTolerance(onsiteG, stationG) {
				vStatus = domain.VarianceOverTolerance
				overTolerance = true
			}
			if _, err := tx.Exec(ctx,
				`UPDATE order_items SET station_weight_g=$2, station_cents=$3, variance_g=$4, variance_status=$5 WHERE id=$1`,
				it.ID, stationG, stationCents, variance, vStatus); err != nil {
				return err
			}
			sg, sc, vg := stationG, stationCents, variance
			it.StationWeightG, it.StationCents, it.VarianceG, it.VarianceStatus = &sg, &sc, &vg, vStatus
			finalTotal += stationCents
			if variance < 0 {
				totalVariance += -variance
			} else {
				totalVariance += variance
			}
			varianceDetails = append(varianceDetails, map[string]any{
				"category": w.Category, "onsite_g": onsiteG, "station_g": stationG,
				"variance_g": variance, "variance_status": vStatus,
			})
		}
		// 未复称的品类按 0 计
		for _, it := range items {
			if !seen[it.Category] && it.StationWeightG == nil {
				onsiteG := int64(0)
				if it.OnsiteWeightG != nil {
					onsiteG = *it.OnsiteWeightG
				}
				vStatus := domain.VarianceOK
				if onsiteG > 0 {
					vStatus = domain.VarianceOverTolerance
					overTolerance = true
				}
				zero := int64(0)
				neg := -onsiteG
				if _, err := tx.Exec(ctx,
					`UPDATE order_items SET station_weight_g=0, station_cents=0, variance_g=$2, variance_status=$3 WHERE id=$1`,
					it.ID, neg, vStatus); err != nil {
					return err
				}
				it.StationWeightG, it.StationCents, it.VarianceG, it.VarianceStatus = &zero, &zero, &neg, vStatus
				totalVariance += onsiteG
			}
		}

		// 入库记录
		inStatus := "CONFIRMED"
		if overTolerance {
			inStatus = "VARIANCE"
		}
		inbound := &domain.InboundRecord{
			OrderID: o.ID, StationID: station.ID, OperatorID: stationUserID,
			Status: inStatus, VarianceG: totalVariance,
		}
		if err := store.CreateInboundRecord(ctx, tx, inbound); err != nil {
			return err
		}
		if err := store.AddConfirmation(ctx, tx, &domain.Confirmation{
			OrderID: o.ID, Kind: "STATION_REWEIGH", ActorRole: domain.RoleStation,
			ActorID: stationUserID, Method: domain.MethodSelf,
			Payload: map[string]any{"items": varianceDetails, "inbound_id": inbound.ID},
		}); err != nil {
			return err
		}

		// 超差影响后续结算: 扣减回收员收益 + 积分差额调整
		var pointsAdjust int64
		if overTolerance {
			penalty := o.OnsiteCents - finalTotal
			if penalty < 0 {
				penalty = -penalty
			}
			if err := s.applyEarningPenalty(ctx, tx, o, penalty); err != nil {
				return err
			}
			if o.SettlementType != nil && *o.SettlementType == domain.SettlePoints {
				// 按最终金额重算应得积分, 差额调整(多退少补)
				expected := domain.PointsForAmount(finalTotal, s.Cfg.PointsPerYuan, s.Cfg.PointsBonusPM)
				st, err := store.LatestSettlement(ctx, tx, o.ID)
				if err == nil && st.Status == domain.SettleSuccess {
					pointsAdjust = expected - st.Points
					if pointsAdjust != 0 {
						if _, _, err := store.CreditPoints(ctx, tx, o.ResidentID, &o.ID, &st.ID,
							pointsAdjust, "VARIANCE_ADJUST", fmt.Sprintf("variance:order:%d", o.ID)); err != nil {
							return err
						}
					}
				}
			}
		}

		// 误收不可回收物: 环保风险记录 + 处理成本(订单收益优先, 不足挂培训记录)
		riskDetails := []map[string]any{}
		var misacceptCostTotal int64
		for _, c := range contaminants {
			if c.ItemName == "" {
				return badReq("contaminant item_name required")
			}
			if c.WeightKg < 0 || c.WeightKg > 1000 {
				return badReq("invalid contaminant weight for %s", c.ItemName)
			}
			if c.HandlingCostYuan < 0 || c.HandlingCostYuan > 100000 {
				return badReq("invalid handling cost for %s", c.ItemName)
			}
			code := c.Code
			if code == "" {
				code = domain.ContamOtherNonRecyclable
			}
			catalog, err := store.RejectionCatalogByCode(ctx, tx, code)
			if err != nil {
				return badReq("unknown contaminant code %s", code)
			}
			costCents := domain.YuanToCents(c.HandlingCostYuan)
			riskDesc := catalog.RiskDesc
			if c.RiskNote != "" {
				riskDesc = catalog.RiskDesc + "；入库备注: " + c.RiskNote
			}
			// 关联同品类明细
			var itemID *int64
			for _, it := range items {
				if c.ItemCategory != "" && it.Category == c.ItemCategory {
					id := it.ID
					itemID = &id
					break
				}
			}
			rec := &domain.ContaminationRecord{
				OrderID: o.ID, ItemID: itemID, ItemCategory: c.ItemCategory,
				ItemName: c.ItemName, ContaminantCode: catalog.Code,
				DetectedStage: domain.StageInbound, Status: domain.ContamErroneousAccepted,
				WeightG: domain.KgToGrams(c.WeightKg), RiskLevel: catalog.RiskLevel,
				RiskDesc: riskDesc, RejectionBasis: catalog.Basis, DisposalAdvice: catalog.Advice,
				HandlingCostCents: costCents, StationID: &station.ID,
				DetectorID: stationUserID, DetectorRole: domain.RoleStation,
			}
			if err := store.CreateContaminationRecord(ctx, tx, rec); err != nil {
				return err
			}

			// 结算扣款: 优先该订单回收员收益; 无收益或不足部分 -> 培训记录挂账
			orderPart, trainingPart := int64(0), int64(0)
			var trainingID *int64
			if costCents > 0 {
				if o.CollectorID != nil {
					if e, gerr := store.EarningByOrder(ctx, tx, o.ID); gerr == nil {
						orderPart, trainingPart = domain.AllocateHandlingCost(costCents, e.TotalCents)
						if orderPart > 0 {
							e.MisacceptPenaltyCents += orderPart
							e.TotalCents -= orderPart
							if err := store.CreateEarning(ctx, tx, e); err != nil {
								return err
							}
						}
					} else if gerr == store.ErrNotFound {
						trainingPart = costCents // 订单无收益(异常单), 全额挂培训
					} else {
						return gerr
					}
				} else {
					trainingPart = costCents
				}
				if trainingPart > 0 {
					t := &domain.CollectorTrainingRecord{
						Kind: domain.TrainingKindMisaccept,
						Title: fmt.Sprintf("误收不可回收物「%s」纠偏培训", c.ItemName),
						Content: fmt.Sprintf(
							"订单 %s 入库时发现误收%s(%s, %s风险), 处理成本 %.2f 元中订单收益不足的 %.2f 元挂培训记录, 完成危废识别培训后销项。",
							o.OrderNo, c.ItemName, catalog.Name, catalog.RiskLevel,
							domain.CentsToYuan(costCents), domain.CentsToYuan(trainingPart)),
						Status: domain.TrainingAssigned, CostCents: costCents,
						ChargedCents: trainingPart, ChargeTarget: domain.ChargeTraining,
					}
					t.CollectorID = 0
					if o.CollectorID != nil {
						t.CollectorID = *o.CollectorID
						oid := o.ID
						t.OrderID = &oid
					}
					if t.CollectorID != 0 {
						if err := store.CreateTrainingRecord(ctx, tx, t); err != nil {
							return err
						}
						tid := t.ID
						trainingID = &tid
					}
				}
			}
			chargedTo := ""
			switch {
			case orderPart > 0 && trainingPart > 0:
				chargedTo = domain.ChargeOrder + "," + domain.ChargeTraining
			case orderPart > 0:
				chargedTo = domain.ChargeOrder
			case trainingPart > 0:
				chargedTo = domain.ChargeTraining
			}
			if err := store.FinalizeInboundContam(ctx, tx, rec.ID, costCents,
				orderPart, trainingPart, chargedTo, trainingID); err != nil {
				return err
			}
			rec.HandlingCostCents, rec.OrderChargedCents = costCents, orderPart
			rec.TrainingChargedCents, rec.ChargedTo, rec.TrainingID = trainingPart, chargedTo, trainingID
			misacceptCostTotal += costCents
			riskRecords = append(riskRecords, rec)
			riskDetails = append(riskDetails, map[string]any{
				"contamination_id": rec.ID, "item_name": c.ItemName,
				"item_category": c.ItemCategory, "code": catalog.Code,
				"risk_level": catalog.RiskLevel, "weight_g": rec.WeightG,
				"handling_cost_yuan": domain.CentsToYuan(costCents),
				"order_charged_yuan": domain.CentsToYuan(orderPart),
				"training_charged_yuan": domain.CentsToYuan(trainingPart),
				"charged_to": chargedTo, "training_id": trainingID,
			})
		}

		// 环保凭证: 按品类(复称有效重量)
		certs := []map[string]any{}
		for _, it := range items {
			if it.StationWeightG == nil || *it.StationWeightG <= 0 {
				continue
			}
			_, co2PerKg, err := store.CategoryInfo(ctx, tx, it.Category)
			if err != nil {
				return err
			}
			effG := domain.EffectiveWeightG(*it.StationWeightG, it.ContaminationPM)
			cert := &domain.Certificate{
				CertNo:     fmt.Sprintf("EC%d-%s", o.ID, it.Category),
				OrderID:    o.ID,
				ResidentID: o.ResidentID,
				Category:   it.Category,
				WeightG:    effG,
				CO2SavedG:  domain.CO2SavedG(effG, int64(co2PerKg)),
			}
			if err := store.CreateCertificate(ctx, tx, cert); err != nil {
				return err
			}
			certs = append(certs, map[string]any{
				"cert_no": cert.CertNo, "category": it.Category,
				"weight_g": effG, "co2_saved_g": cert.CO2SavedG,
			})
		}

		// DELIVERED -> INBOUND_CONFIRMED -> COMPLETED
		if err := s.transit(ctx, tx, o, domain.StatusInboundConfirmed, domain.RoleStation, stationUserID,
			map[string]any{
				"inbound_id": inbound.ID, "final_cents": finalTotal,
				"over_tolerance": overTolerance, "variance_g": totalVariance,
				"points_adjust": pointsAdjust,
				"env_risks": riskDetails, "misaccept_handling_cost_cents": misacceptCostTotal,
			},
			map[string]any{"final_cents": finalTotal}); err != nil {
			return err
		}
		if err := s.transit(ctx, tx, o, domain.StatusCompleted, domain.RoleSystem, 0,
			map[string]any{"certificates": certs, "env_risks": riskDetails}, nil); err != nil {
			return err
		}
		o.FinalCents = finalTotal
		// 通知居民
		if err := s.notify(ctx, tx, o.ResidentID, "ORDER_COMPLETED",
			fmt.Sprintf("订单 %s 已完成, 最终金额 %.2f 元, 已开具 %d 张环保凭证",
				o.OrderNo, domain.CentsToYuan(finalTotal), len(certs))); err != nil {
			return err
		}
		// 误收环保风险: 通知回收员(培训挂账/扣款)
		if len(riskRecords) > 0 && o.CollectorID != nil {
			if err := s.notify(ctx, tx, *o.CollectorID, "ENV_RISK_MISACCEPT",
				fmt.Sprintf("订单 %s 入库发现 %d 件误收不可回收物, 处理成本合计 %.2f 元, 详见培训记录",
					o.OrderNo, len(riskRecords), domain.CentsToYuan(misacceptCostTotal))); err != nil {
				return err
			}
		}
		return nil
	})
	return o, items, riskRecords, err
}

// applyEarningPenalty 复称超差扣减回收员收益
func (s *Service) applyEarningPenalty(ctx context.Context, tx pgx.Tx, o *domain.Order, penalty int64) error {
	e, err := store.EarningByOrder(ctx, tx, o.ID)
	if err != nil {
		if err == store.ErrNotFound {
			return nil // 无收益记录(异常场景), 跳过
		}
		return err
	}
	if penalty > e.TotalCents {
		penalty = e.TotalCents
	}
	e.PenaltyCents += penalty
	e.TotalCents -= penalty
	return store.CreateEarning(ctx, tx, e) // UPSERT by order_id
}
