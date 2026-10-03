package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"recycle/internal/domain"
)

// ---- hazmat disposal points ----

// CreateHazmatPoint seed/管理用: 新建处理点
func CreateHazmatPoint(ctx context.Context, q Querier, p *domain.HazmatDisposalPoint) error {
	accepts, _ := json.Marshal(p.Accepts)
	return q.QueryRow(ctx,
		`INSERT INTO hazmat_disposal_points (name, street, address, lat, lng, accepts, open_hours, phone)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		p.Name, p.Street, p.Address, p.Lat, p.Lng, string(accepts), p.OpenHours, p.Phone).
		Scan(&p.ID, &p.CreatedAt)
}

// ListHazmatPoints 列出处理点, 可按街道过滤
func ListHazmatPoints(ctx context.Context, q Querier, street string) ([]*domain.HazmatDisposalPoint, error) {
	sql := `SELECT id, name, street, address, lat, lng, accepts, open_hours, phone, created_at
	        FROM hazmat_disposal_points`
	args := []any{}
	if street != "" {
		sql += ` WHERE street=$1`
		args = append(args, street)
	}
	sql += ` ORDER BY id`
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHazmatPoints(rows)
}

func scanHazmatPoints(rows pgx.Rows) ([]*domain.HazmatDisposalPoint, error) {
	var out []*domain.HazmatDisposalPoint
	for rows.Next() {
		p := &domain.HazmatDisposalPoint{}
		var accepts []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Street, &p.Address, &p.Lat, &p.Lng,
			&accepts, &p.OpenHours, &p.Phone, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Accepts = []string{}
		_ = json.Unmarshal(accepts, &p.Accepts)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- rejected items (现场拒收) ----

func CreateRejectedItem(ctx context.Context, q Querier, r *domain.RejectedItem) error {
	return q.QueryRow(ctx,
		`INSERT INTO rejected_items
		 (order_id, collector_id, category, item_name, hazmat_code, risk_level, risk_note,
		  refusal_basis, refusal_text, disposal_advice, resident_insisted, status, nearest_point_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id, created_at`,
		r.OrderID, r.CollectorID, r.Category, r.ItemName, r.HazmatCode, r.RiskLevel, r.RiskNote,
		r.RefusalBasis, r.RefusalText, r.DisposalAdvice, r.ResidentInsisted, r.Status, r.NearestPointID).
		Scan(&r.ID, &r.CreatedAt)
}

func ListRejectedItemsByOrder(ctx context.Context, q Querier, orderID int64) ([]*domain.RejectedItem, error) {
	rows, err := q.Query(ctx,
		`SELECT id, order_id, collector_id, category, item_name, hazmat_code, risk_level, risk_note,
		        refusal_basis, refusal_text, disposal_advice, resident_insisted, status, nearest_point_id, created_at
		 FROM rejected_items WHERE order_id=$1 ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.RejectedItem
	for rows.Next() {
		r := &domain.RejectedItem{}
		if err := rows.Scan(&r.ID, &r.OrderID, &r.CollectorID, &r.Category, &r.ItemName,
			&r.HazmatCode, &r.RiskLevel, &r.RiskNote, &r.RefusalBasis, &r.RefusalText,
			&r.DisposalAdvice, &r.ResidentInsisted, &r.Status, &r.NearestPointID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- station hazmat records (误收异常) ----

func CreateStationHazmat(ctx context.Context, q Querier, r *domain.StationHazmatRecord) error {
	return q.QueryRow(ctx,
		`INSERT INTO station_hazmat_records
		 (order_id, station_id, operator_id, collector_id, category, item_name, hazmat_code,
		  risk_level, risk_note, weight_g, handling_cost_cents, deduct_target,
		  earning_deduct_cents, training_id, inbound_id, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id, created_at`,
		r.OrderID, r.StationID, r.OperatorID, r.CollectorID, r.Category, r.ItemName, r.HazmatCode,
		r.RiskLevel, r.RiskNote, r.WeightG, r.HandlingCostCents, r.DeductTarget,
		r.EarningDeductCents, r.TrainingID, r.InboundID, r.Status).
		Scan(&r.ID, &r.CreatedAt)
}

func ListStationHazmat(ctx context.Context, q Querier, stationID int64, limit int) ([]*domain.StationHazmatRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id, order_id, station_id, operator_id, collector_id, category, item_name, hazmat_code,
		        risk_level, risk_note, weight_g, handling_cost_cents, deduct_target,
		        earning_deduct_cents, training_id, inbound_id, status, created_at
		 FROM station_hazmat_records
		 WHERE ($1=0 OR station_id=$1) ORDER BY id DESC LIMIT $2`, stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationHazmat(rows)
}

// ListStationHazmatByCollector 回收员自己的误收异常(供培训/绩效查看)
func ListStationHazmatByCollector(ctx context.Context, q Querier, collectorID int64, limit int) ([]*domain.StationHazmatRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id, order_id, station_id, operator_id, collector_id, category, item_name, hazmat_code,
		        risk_level, risk_note, weight_g, handling_cost_cents, deduct_target,
		        earning_deduct_cents, training_id, inbound_id, status, created_at
		 FROM station_hazmat_records WHERE collector_id=$1 ORDER BY id DESC LIMIT $2`, collectorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationHazmat(rows)
}

func scanStationHazmat(rows pgx.Rows) ([]*domain.StationHazmatRecord, error) {
	var out []*domain.StationHazmatRecord
	for rows.Next() {
		r := &domain.StationHazmatRecord{}
		if err := rows.Scan(&r.ID, &r.OrderID, &r.StationID, &r.OperatorID, &r.CollectorID,
			&r.Category, &r.ItemName, &r.HazmatCode, &r.RiskLevel, &r.RiskNote, &r.WeightG,
			&r.HandlingCostCents, &r.DeductTarget, &r.EarningDeductCents, &r.TrainingID,
			&r.InboundID, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountStationHazmatForOrder 该订单是否已登记过误收异常(幂等防护)
func CountStationHazmatForOrder(ctx context.Context, q Querier, orderID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM station_hazmat_records WHERE order_id=$1`, orderID).Scan(&n)
	return n, err
}

// ListStationHazmatByOrder 订单维度的误收异常
func ListStationHazmatByOrder(ctx context.Context, q Querier, orderID int64) ([]*domain.StationHazmatRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id, order_id, station_id, operator_id, collector_id, category, item_name, hazmat_code,
		        risk_level, risk_note, weight_g, handling_cost_cents, deduct_target,
		        earning_deduct_cents, training_id, inbound_id, status, created_at
		 FROM station_hazmat_records WHERE order_id=$1 ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationHazmat(rows)
}

// ---- collector training records ----

func CreateTrainingRecord(ctx context.Context, q Querier, t *domain.CollectorTrainingRecord) error {
	return q.QueryRow(ctx,
		`INSERT INTO collector_training_records
		 (collector_id, order_id, hazmat_record_id, kind, reason, deduct_cents, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`,
		t.CollectorID, t.OrderID, t.HazmatRecordID, t.Kind, t.Reason, t.DeductCents, t.Status).
		Scan(&t.ID, &t.CreatedAt)
}

func ListTrainingRecords(ctx context.Context, q Querier, collectorID int64, limit int) ([]*domain.CollectorTrainingRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id, collector_id, order_id, hazmat_record_id, kind, reason, deduct_cents, status, created_at, completed_at
		 FROM collector_training_records
		 WHERE ($1=0 OR collector_id=$1) ORDER BY id DESC LIMIT $2`, collectorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CollectorTrainingRecord
	for rows.Next() {
		t := &domain.CollectorTrainingRecord{}
		if err := rows.Scan(&t.ID, &t.CollectorID, &t.OrderID, &t.HazmatRecordID, &t.Kind,
			&t.Reason, &t.DeductCents, &t.Status, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- earnings: 危废误收异常扣款 ----

// ApplyHazmatEarningDeduct 在回收员订单收益上叠加危废异常扣款(金额由调用方 clamp 到可扣上限)
func ApplyHazmatEarningDeduct(ctx context.Context, q Querier, orderID int64, cents int64) error {
	tag, err := q.Exec(ctx,
		`UPDATE collector_earnings
		 SET hazmat_deduct_cents=hazmat_deduct_cents+$2,
		     total_cents=GREATEST(0, total_cents-$2)
		 WHERE order_id=$1`, orderID, cents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
