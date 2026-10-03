-- 不可回收物混入处理: 现场拒收 / 误收入库 / 拒收目录 / 附近处理点 / 回收员培训扣款
-- 金额单位: 分(cents); 重量单位: 克(grams)

-- 拒收依据目录(有害/非可回收物的风险、依据条款、正确投放建议)
CREATE TABLE IF NOT EXISTS rejection_catalog (
    code          TEXT PRIMARY KEY,          -- paint_bucket | other_non_recyclable
    name          TEXT NOT NULL,
    disposal_type TEXT NOT NULL,             -- hazardous_waste | general_waste
    risk_level    TEXT NOT NULL,             -- LOW | MEDIUM | HIGH
    risk_desc     TEXT NOT NULL,             -- 风险说明
    basis         TEXT NOT NULL,             -- 拒收依据(法规/目录条款)
    advice        TEXT NOT NULL              -- 正确投放建议
);

-- 规范处理点(有害垃圾定点收集点等)
CREATE TABLE IF NOT EXISTS disposal_points (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    accepts    JSONB NOT NULL DEFAULT '[]',  -- 接收类型: ["hazardous_waste"]
    street     TEXT NOT NULL DEFAULT '',
    address    TEXT NOT NULL DEFAULT '',
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    open_hours TEXT NOT NULL DEFAULT '',
    phone      TEXT NOT NULL DEFAULT ''
);

-- 不可回收物混入记录: 现场拒收(ONSITE) 与 入库误收(INBOUND) 两类
CREATE TABLE IF NOT EXISTS contamination_records (
    id                   BIGSERIAL PRIMARY KEY,
    order_id             BIGINT NOT NULL REFERENCES orders(id),
    item_id              BIGINT,                       -- 关联订单明细(混入的可回收品类行)
    item_category        TEXT NOT NULL DEFAULT '',     -- 混入的可回收品类, 如 metal
    item_name            TEXT NOT NULL,                -- 不可回收物名称, 如 油漆桶
    contaminant_code     TEXT NOT NULL DEFAULT 'other_non_recyclable',
    detected_stage       TEXT NOT NULL,                -- ONSITE | INBOUND
    status               TEXT NOT NULL,                -- MARKED | REJECTED | ERRONEOUS_ACCEPTED
    weight_g             BIGINT NOT NULL DEFAULT 0,
    risk_level           TEXT NOT NULL DEFAULT 'MEDIUM',
    risk_desc            TEXT NOT NULL DEFAULT '',
    rejection_basis      TEXT NOT NULL DEFAULT '',
    disposal_advice      TEXT NOT NULL DEFAULT '',
    resident_insists     BOOLEAN NOT NULL DEFAULT FALSE,
    handling_cost_cents  BIGINT NOT NULL DEFAULT 0,    -- 站点入库人员标记的处理成本
    order_charged_cents  BIGINT NOT NULL DEFAULT 0,    -- 从该订单收益中扣除
    training_charged_cents BIGINT NOT NULL DEFAULT 0,  -- 不足部分挂培训记录扣除
    charged_to           TEXT NOT NULL DEFAULT '',     -- ORDER | TRAINING | ORDER,TRAINING
    training_id          BIGINT,
    station_id           BIGINT,
    detector_id          BIGINT NOT NULL,
    detector_role        TEXT NOT NULL,
    resolved_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_contam_order ON contamination_records(order_id);
CREATE INDEX IF NOT EXISTS idx_contam_stage_status ON contamination_records(detected_stage, status);
CREATE INDEX IF NOT EXISTS idx_contam_station ON contamination_records(station_id);

-- 回收员培训记录(误收留痕 + 挂账扣款; 结算时在收益单或本记录体现)
CREATE TABLE IF NOT EXISTS collector_training_records (
    id            BIGSERIAL PRIMARY KEY,
    collector_id  BIGINT NOT NULL,
    order_id      BIGINT,
    kind          TEXT NOT NULL DEFAULT 'MISACCEPT', -- MISACCEPT(误收不可回收物)
    title         TEXT NOT NULL,
    content       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ASSIGNED',  -- ASSIGNED | COMPLETED
    cost_cents    BIGINT NOT NULL DEFAULT 0,         -- 站点标记处理成本
    charged_cents BIGINT NOT NULL DEFAULT 0,         -- 实际从培训/挂账中扣除金额
    charge_target TEXT NOT NULL DEFAULT '',          -- ORDER | TRAINING | ORDER,TRAINING
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_training_collector ON collector_training_records(collector_id, id);
CREATE INDEX IF NOT EXISTS idx_training_order ON collector_training_records(order_id);

-- 回收员收益增加"误收异常扣款"列(与复称超差扣罚分开记账, 便于结算追溯)
ALTER TABLE collector_earnings
    ADD COLUMN IF NOT EXISTS misaccept_penalty_cents BIGINT NOT NULL DEFAULT 0;
