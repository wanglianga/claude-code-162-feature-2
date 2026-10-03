package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"recycle/internal/domain"
)

// ---- rejection catalog ----

func RejectionCatalogByCode(ctx context.Context, q Querier, code string) (*domain.RejectionCatalogItem, error) {
	c := &domain.RejectionCatalogItem{}
	err := q.QueryRow(ctx,
		`SELECT code, name, disposal_type, risk_level, risk_desc, basis, advice
		 FROM rejection_catalog WHERE code=$1`, code).
		Scan(&c.Code, &c.Name, &c.DisposalType, &c.RiskLevel, &c.RiskDesc, &c.Basis, &c.Advice)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return c, err
}

func ListRejectionCatalog(ctx context.Context, q Querier) ([]*domain.RejectionCatalogItem, error) {
	rows, err := q.Query(ctx,
		`SELECT code, name, disposal_type, risk_level, risk_desc, basis, advice
		 FROM rejection_catalog ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.RejectionCatalogItem
	for rows.Next() {
		c := &domain.RejectionCatalogItem{}
		if err := rows.Scan(&c.Code, &c.Name, &c.DisposalType, &c.RiskLevel,
			&c.RiskDesc, &c.Basis, &c.Advice); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- disposal points ----

func CreateDisposalPoint(ctx context.Context, q Querier, p *domain.DisposalPoint) error {
	accepts, _ := json.Marshal(p.Accepts)
	return q.QueryRow(ctx,
		`INSERT INTO disposal_points (name, accepts, street, address, lat, lng, open_hours, phone)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		p.Name, accepts, p.Street, p.Address, p.Lat, p.Lng, p.OpenHours, p.Phone).
		Scan(&p.ID)
}

func ListDisposalPoints(ctx context.Context, q Querier) ([]*domain.DisposalPoint, error) {
	rows, err := q.Query(ctx,
		`SELECT id, name, accepts, street, address, lat, lng, open_hours, phone
		 FROM disposal_points ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.DisposalPoint
	for rows.Next() {
		p, err := scanDisposalPoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanDisposalPoint(row pgx.Row) (*domain.DisposalPoint, error) {
	p := &domain.DisposalPoint{}
	var accepts []byte
	if err := row.Scan(&p.ID, &p.Name, &accepts, &p.Street, &p.Address,
		&p.Lat, &p.Lng, &p.OpenHours, &p.Phone); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(accepts, &p.Accepts)
	return p, nil
}

// ---- contamination records ----

func CreateContaminationRecord(ctx context.Context, q Querier, r *domain.ContaminationRecord) error {
	return q.QueryRow(ctx,
		`INSERT INTO contamination_records
		 (order_id, item_id, item_category, item_name, contaminant_code, detected_stage, status,
		  weight_g, risk_level, risk_desc, rejection_basis, disposal_advice, resident_insists,
		  handling_cost_cents, order_charged_cents, training_charged_cents, charged_to,
		  training_id, station_id, detector_id, detector_role)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		 RETURNING id, created_at`,
		r.OrderID, r.ItemID, r.ItemCategory, r.ItemName, r.ContaminantCode, r.DetectedStage, r.Status,
		r.WeightG, r.RiskLevel, r.RiskDesc, r.RejectionBasis, r.DisposalAdvice, r.ResidentInsists,
		r.HandlingCostCents, r.OrderChargedCents, r.TrainingChargedCents, r.ChargedTo,
		r.TrainingID, r.StationID, r.DetectorID, r.DetectorRole).
		Scan(&r.ID, &r.CreatedAt)
}

const contamCols = `id, order_id, item_id, item_category, item_name, contaminant_code, detected_stage,
	status, weight_g, risk_level, risk_desc, rejection_basis, disposal_advice, resident_insists,
	handling_cost_cents, order_charged_cents, training_charged_cents, charged_to, training_id,
	station_id, detector_id, detector_role, resolved_at, created_at`

func scanContam(row pgx.Row) (*domain.ContaminationRecord, error) {
	r := &domain.ContaminationRecord{}
	err := row.Scan(&r.ID, &r.OrderID, &r.ItemID, &r.ItemCategory, &r.ItemName,
		&r.ContaminantCode, &r.DetectedStage, &r.Status, &r.WeightG, &r.RiskLevel,
		&r.RiskDesc, &r.RejectionBasis, &r.DisposalAdvice, &r.ResidentInsists,
		&r.HandlingCostCents, &r.OrderChargedCents, &r.TrainingChargedCents, &r.ChargedTo,
		&r.TrainingID, &r.StationID, &r.DetectorID, &r.DetectorRole, &r.ResolvedAt, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

func ContaminationRecordByID(ctx context.Context, q Querier, id int64) (*domain.ContaminationRecord, error) {
	return scanContam(q.QueryRow(ctx, `SELECT `+contamCols+` FROM contamination_records WHERE id=$1`, id))
}

func ContaminationRecordByIDForUpdate(ctx context.Context, q Querier, id int64) (*domain.ContaminationRecord, error) {
	return scanContam(q.QueryRow(ctx, `SELECT `+contamCols+` FROM contamination_records WHERE id=$1 FOR UPDATE`, id))
}

func ListContaminationByOrder(ctx context.Context, q Querier, orderID int64) ([]*domain.ContaminationRecord, error) {
	rows, err := q.Query(ctx, `SELECT `+contamCols+` FROM contamination_records WHERE order_id=$1 ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ContaminationRecord
	for rows.Next() {
		r, err := scanContam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListEnvRisks 站点环保风险记录(误收, 可按 station 过滤)
func ListEnvRisks(ctx context.Context, q Querier, stationID int64, limit int) ([]*domain.ContaminationRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT `+contamCols+` FROM contamination_records
		 WHERE detected_stage='INBOUND' AND ($1=0 OR station_id=$1)
		 ORDER BY id DESC LIMIT $2`, stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ContaminationRecord
	for rows.Next() {
		r, err := scanContam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkContamResidentInsists 居民坚持带走: 回填拒收依据, 保持 MARKED
func MarkContamResidentInsists(ctx context.Context, q Querier, id int64) error {
	_, err := q.Exec(ctx,
		`UPDATE contamination_records SET resident_insists=TRUE WHERE id=$1`, id)
	return err
}

// ResolveContamRejected 拒收闭环: 状态 REJECTED + 时间
func ResolveContamRejected(ctx context.Context, q Querier, id int64) error {
	_, err := q.Exec(ctx,
		`UPDATE contamination_records SET status='REJECTED', resident_insists=TRUE, resolved_at=now()
		 WHERE id=$1 AND status='MARKED'`, id)
	return err
}

// FinalizeInboundContam 入库误收结算: 处理成本、扣款去向、培训记录
func FinalizeInboundContam(ctx context.Context, q Querier, id int64,
	handlingCost, orderCharged, trainingCharged int64, chargedTo string, trainingID *int64) error {
	_, err := q.Exec(ctx,
		`UPDATE contamination_records SET handling_cost_cents=$2, order_charged_cents=$3,
		 training_charged_cents=$4, charged_to=$5, training_id=$6, resolved_at=now()
		 WHERE id=$1`,
		id, handlingCost, orderCharged, trainingCharged, chargedTo, trainingID)
	return err
}

// ---- collector training records ----

func CreateTrainingRecord(ctx context.Context, q Querier, t *domain.CollectorTrainingRecord) error {
	return q.QueryRow(ctx,
		`INSERT INTO collector_training_records
		 (collector_id, order_id, kind, title, content, status, cost_cents, charged_cents, charge_target)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, created_at`,
		t.CollectorID, t.OrderID, t.Kind, t.Title, t.Content, t.Status,
		t.CostCents, t.ChargedCents, t.ChargeTarget).
		Scan(&t.ID, &t.CreatedAt)
}

func CompleteTrainingRecord(ctx context.Context, q Querier, id int64) error {
	_, err := q.Exec(ctx,
		`UPDATE collector_training_records SET status='COMPLETED', completed_at=now()
		 WHERE id=$1 AND status='ASSIGNED'`, id)
	return err
}

func ListTrainingRecords(ctx context.Context, q Querier, collectorID int64, limit int) ([]*domain.CollectorTrainingRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id, collector_id, order_id, kind, title, content, status, cost_cents,
		        charged_cents, charge_target, created_at, completed_at
		 FROM collector_training_records WHERE collector_id=$1 ORDER BY id DESC LIMIT $2`,
		collectorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CollectorTrainingRecord
	for rows.Next() {
		t := &domain.CollectorTrainingRecord{}
		if err := rows.Scan(&t.ID, &t.CollectorID, &t.OrderID, &t.Kind, &t.Title, &t.Content,
			&t.Status, &t.CostCents, &t.ChargedCents, &t.ChargeTarget, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
