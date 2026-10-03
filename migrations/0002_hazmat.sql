-- 不可回收物(危险废物)混入处理
-- 场景一: 回收员现场识别并拒收(油漆桶混入金属), 居民坚持时给出拒收依据 + 正确投放建议 + 附近处理点
-- 场景二: 回收员误收, 站点入库时登记异常: 处理成本 + 环保风险记录 + 异常扣款(从订单收益或培训记录体现)

-- 回收员收益表增加危废误收异常扣款列(与复称扣罚分开记账)
ALTER TABLE collector_earnings ADD COLUMN IF NOT EXISTS hazmat_deduct_cents BIGINT NOT NULL DEFAULT 0;

-- 附近危废/不可回收物处理点
CREATE TABLE IF NOT EXISTS hazmat_disposal_points (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    street        TEXT NOT NULL,
    address       TEXT NOT NULL,
    lat           DOUBLE PRECISION NOT NULL,
    lng           DOUBLE PRECISION NOT NULL,
    accepts       JSONB NOT NULL DEFAULT '[]',  -- 接收品类, 如 ["paint","battery","lamp","medicine"]
    open_hours    TEXT NOT NULL DEFAULT '',
    phone         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_hazmat_points_street ON hazmat_disposal_points(street);

-- 现场拒收记录: 回收员标记不可回收物 + 风险说明 + 拒收依据 + 投放建议
CREATE TABLE IF NOT EXISTS rejected_items (
    id                BIGSERIAL PRIMARY KEY,
    order_id          BIGINT NOT NULL REFERENCES orders(id),
    collector_id      BIGINT NOT NULL,
    category          TEXT NOT NULL,             -- 混入的回收品类(如 metal)
    item_name         TEXT NOT NULL,             -- 居民声称的物品(如 油漆桶)
    hazmat_code       TEXT NOT NULL,             -- 不可回收物编码(如 paint_bucket)
    risk_level        TEXT NOT NULL,             -- LOW | MEDIUM | HIGH
    risk_note         TEXT NOT NULL,             -- 回收员现场说明的风险
    refusal_basis     TEXT NOT NULL,             -- 拒收依据编码(如 NATIONAL_HW_LIST)
    refusal_text      TEXT NOT NULL,             -- 拒收依据可读文本
    disposal_advice   TEXT NOT NULL,             -- 正确投放建议
    resident_insisted BOOLEAN NOT NULL DEFAULT FALSE, -- 居民是否坚持要求带走
    status            TEXT NOT NULL DEFAULT 'REJECTED', -- REJECTED | TAKEN_BACK(居民自行带走)
    nearest_point_id  BIGINT REFERENCES hazmat_disposal_points(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rejected_items_order ON rejected_items(order_id);
CREATE INDEX IF NOT EXISTS idx_rejected_items_collector ON rejected_items(collector_id, id);

-- 站点入库时发现的误收不可回收物异常记录
CREATE TABLE IF NOT EXISTS station_hazmat_records (
    id              BIGSERIAL PRIMARY KEY,
    order_id        BIGINT NOT NULL REFERENCES orders(id),
    station_id      BIGINT NOT NULL REFERENCES stations(id),
    operator_id     BIGINT NOT NULL,
    collector_id    BIGINT NOT NULL,             -- 误收的回收员(责任方)
    category        TEXT NOT NULL,               -- 混入的回收品类
    item_name       TEXT NOT NULL,
    hazmat_code     TEXT NOT NULL,
    risk_level      TEXT NOT NULL,               -- LOW | MEDIUM | HIGH
    risk_note       TEXT NOT NULL,               -- 环保风险说明
    weight_g        BIGINT NOT NULL DEFAULT 0,   -- 误收重量
    handling_cost_cents BIGINT NOT NULL,         -- 站点入库人员标记的处理成本(分)
    deduct_target   TEXT NOT NULL,               -- ORDER(从对应订单收益) | TRAINING(生成培训记录)
    earning_deduct_cents BIGINT NOT NULL DEFAULT 0, -- 实际从订单收益扣减金额(分)
    training_id     BIGINT,                      -- 扣款去向为培训记录时关联
    inbound_id      BIGINT REFERENCES inbound_records(id),
    status          TEXT NOT NULL DEFAULT 'CONFIRMED',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_station_hazmat_order ON station_hazmat_records(order_id);
CREATE INDEX IF NOT EXISTS idx_station_hazmat_station ON station_hazmat_records(station_id, id);
CREATE INDEX IF NOT EXISTS idx_station_hazmat_collector ON station_hazmat_records(collector_id, id);

-- 回收员培训记录(误收扣款进入培训/再教育时体现)
CREATE TABLE IF NOT EXISTS collector_training_records (
    id              BIGSERIAL PRIMARY KEY,
    collector_id    BIGINT NOT NULL,
    order_id        BIGINT REFERENCES orders(id),
    hazmat_record_id BIGINT REFERENCES station_hazmat_records(id),
    kind            TEXT NOT NULL,              -- HAZMAT_MISACCEPT | REFRESHER
    reason          TEXT NOT NULL,
    deduct_cents    BIGINT NOT NULL DEFAULT 0,  -- 从培训/绩效记录体现的扣款(分)
    status          TEXT NOT NULL DEFAULT 'OPEN', -- OPEN(待培训) | DONE
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_training_collector ON collector_training_records(collector_id, id);
