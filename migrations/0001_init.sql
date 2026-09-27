-- 区域再生资源回收服务 - 初始 schema
-- 金额单位: 分(cents); 重量单位: 克(grams); 比例: 千分比(per-mille)

CREATE TABLE IF NOT EXISTS users (
    id          BIGSERIAL PRIMARY KEY,
    account     TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('resident','collector','station','street')),
    token       TEXT NOT NULL UNIQUE,
    phone       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS residents (
    user_id      BIGINT PRIMARY KEY REFERENCES users(id),
    street       TEXT NOT NULL,
    address      TEXT NOT NULL,
    lat          DOUBLE PRECISION NOT NULL,
    lng          DOUBLE PRECISION NOT NULL,
    floor        INT NOT NULL DEFAULT 1,
    has_elevator BOOLEAN NOT NULL DEFAULT TRUE,
    is_elderly   BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS stations (
    id      BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    name    TEXT NOT NULL,
    street  TEXT NOT NULL,
    lat     DOUBLE PRECISION NOT NULL,
    lng     DOUBLE PRECISION NOT NULL
);

CREATE TABLE IF NOT EXISTS collectors (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id),
    station_id     BIGINT NOT NULL REFERENCES stations(id),
    on_duty        BOOLEAN NOT NULL DEFAULT FALSE,
    lat            DOUBLE PRECISION NOT NULL DEFAULT 0,
    lng            DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_concurrent INT NOT NULL DEFAULT 6
);

CREATE TABLE IF NOT EXISTS categories (
    code            TEXT PRIMARY KEY, -- paper/plastic/metal/appliance/clothing
    name            TEXT NOT NULL,
    points_per_yuan INT NOT NULL DEFAULT 10,  -- 1元 兑多少积分
    co2_g_per_kg    INT NOT NULL DEFAULT 1000 -- 每公斤回收物减少的 CO2 克数
);

-- 市场行情(版本化, 取 effective_from 最新者)
CREATE TABLE IF NOT EXISTS market_prices (
    id             BIGSERIAL PRIMARY KEY,
    category       TEXT NOT NULL REFERENCES categories(code),
    price_cents_kg BIGINT NOT NULL, -- 元/kg 的分表示
    effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by     TEXT NOT NULL DEFAULT 'system'
);
CREATE INDEX IF NOT EXISTS idx_market_prices_cat ON market_prices(category, effective_from DESC);

-- 回收员排班
CREATE TABLE IF NOT EXISTS schedules (
    id           BIGSERIAL PRIMARY KEY,
    collector_id BIGINT NOT NULL REFERENCES collectors(user_id),
    work_date    DATE NOT NULL,
    start_hour   INT NOT NULL,
    end_hour     INT NOT NULL,
    capacity     INT NOT NULL DEFAULT 8,
    UNIQUE (collector_id, work_date, start_hour)
);

-- 回收订单(预约单)
CREATE TABLE IF NOT EXISTS orders (
    id                BIGSERIAL PRIMARY KEY,
    order_no          TEXT NOT NULL UNIQUE,
    resident_id       BIGINT NOT NULL REFERENCES residents(user_id),
    collector_id      BIGINT REFERENCES collectors(user_id),
    station_id        BIGINT REFERENCES stations(id),
    status            TEXT NOT NULL,
    street            TEXT NOT NULL,
    address           TEXT NOT NULL,
    lat               DOUBLE PRECISION NOT NULL,
    lng               DOUBLE PRECISION NOT NULL,
    floor             INT NOT NULL,
    has_elevator      BOOLEAN NOT NULL,
    time_window_start TIMESTAMPTZ NOT NULL,
    time_window_end   TIMESTAMPTZ NOT NULL,
    photos            JSONB NOT NULL DEFAULT '[]',
    verify_code       TEXT NOT NULL,             -- 居民出示给回收员的核验码
    estimate_cents    BIGINT NOT NULL DEFAULT 0, -- 预估总额(下单快照)
    onsite_cents      BIGINT NOT NULL DEFAULT 0, -- 现场定价总额
    final_cents       BIGINT NOT NULL DEFAULT 0, -- 站点复称后最终金额
    settlement_type   TEXT,                      -- cash | points
    idempotency_key   TEXT UNIQUE,
    late_minutes      INT NOT NULL DEFAULT 0,
    cancel_reason     TEXT,
    version           INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_orders_resident ON orders(resident_id);
CREATE INDEX IF NOT EXISTS idx_orders_collector ON orders(collector_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);

-- 订单品类明细: est(预约) / onsite(现场) / station(复称) 三段重量价格
CREATE TABLE IF NOT EXISTS order_items (
    id                 BIGSERIAL PRIMARY KEY,
    order_id           BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    category           TEXT NOT NULL REFERENCES categories(code),
    source             TEXT NOT NULL DEFAULT 'BOOKING', -- BOOKING | ONSITE_ADDED
    est_weight_g       BIGINT NOT NULL DEFAULT 0,
    est_price_cents    BIGINT NOT NULL DEFAULT 0, -- 下单时锁定的单价(分/kg)
    est_cents          BIGINT NOT NULL DEFAULT 0,
    onsite_weight_g    BIGINT,
    contamination_pm   INT NOT NULL DEFAULT 0,    -- 混入不可回收物扣杂千分比
    onsite_price_cents BIGINT,
    onsite_cents       BIGINT,
    station_weight_g   BIGINT,
    station_cents      BIGINT,
    variance_g         BIGINT,                    -- 复称差异(站-现场)
    variance_status    TEXT NOT NULL DEFAULT 'NONE', -- NONE|OK|OVER_TOLERANCE
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);

-- 状态流转事件(审计日志, 只追加)
CREATE TABLE IF NOT EXISTS order_events (
    id          BIGSERIAL PRIMARY KEY,
    order_id    BIGINT NOT NULL REFERENCES orders(id),
    from_status TEXT,
    to_status   TEXT NOT NULL,
    actor_role  TEXT NOT NULL,
    actor_id    BIGINT NOT NULL DEFAULT 0,
    detail      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_order_events_order ON order_events(order_id, id);

-- 确认记录(谁确认/何时/何种方式)
CREATE TABLE IF NOT EXISTS confirmations (
    id         BIGSERIAL PRIMARY KEY,
    order_id   BIGINT NOT NULL REFERENCES orders(id),
    kind       TEXT NOT NULL, -- RESIDENT_VERIFY|WEIGH_CONFIRM|SETTLE_CONFIRM|STATION_REWEIGH
    actor_role TEXT NOT NULL,
    actor_id   BIGINT NOT NULL,
    method     TEXT NOT NULL, -- SCAN_CODE|SELF|ASSISTED|SYSTEM
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_confirmations_order ON confirmations(order_id, id);

-- 结算单
CREATE TABLE IF NOT EXISTS settlements (
    id              BIGSERIAL PRIMARY KEY,
    order_id        BIGINT NOT NULL REFERENCES orders(id),
    type            TEXT NOT NULL, -- cash | points
    amount_cents    BIGINT NOT NULL,
    points          BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL, -- PENDING|SUCCESS|FAILED
    fail_reason     TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    retry_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_settlements_order ON settlements(order_id);

-- 积分账户(余额) + 积分流水(不可变, 幂等键唯一)
CREATE TABLE IF NOT EXISTS points_accounts (
    resident_id BIGINT PRIMARY KEY REFERENCES residents(user_id),
    balance     BIGINT NOT NULL DEFAULT 0,
    version     INT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS points_entries (
    id              BIGSERIAL PRIMARY KEY,
    resident_id     BIGINT NOT NULL,
    order_id        BIGINT,
    settlement_id   BIGINT,
    delta           BIGINT NOT NULL,
    balance_after   BIGINT NOT NULL,
    kind            TEXT NOT NULL, -- RECYCLE_CREDIT|VARIANCE_ADJUST|CLAIM_RECREDIT
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_points_entries_resident ON points_entries(resident_id, id);

-- 积分不到账申诉
CREATE TABLE IF NOT EXISTS points_claims (
    id          BIGSERIAL PRIMARY KEY,
    resident_id BIGINT NOT NULL,
    order_id    BIGINT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'OPEN', -- OPEN|RESOLVED_CREDITED|ALREADY_CREDITED
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

-- 站点入库复称记录
CREATE TABLE IF NOT EXISTS inbound_records (
    id           BIGSERIAL PRIMARY KEY,
    order_id     BIGINT NOT NULL REFERENCES orders(id),
    station_id   BIGINT NOT NULL REFERENCES stations(id),
    operator_id  BIGINT NOT NULL,
    status       TEXT NOT NULL, -- CONFIRMED|VARIANCE
    variance_g   BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbound_order ON inbound_records(order_id);

-- 环保凭证
CREATE TABLE IF NOT EXISTS certificates (
    id           BIGSERIAL PRIMARY KEY,
    cert_no      TEXT NOT NULL UNIQUE,
    order_id     BIGINT NOT NULL REFERENCES orders(id),
    resident_id  BIGINT NOT NULL,
    category     TEXT NOT NULL,
    weight_g     BIGINT NOT NULL,
    co2_saved_g  BIGINT NOT NULL,
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_certificates_order ON certificates(order_id);
CREATE INDEX IF NOT EXISTS idx_certificates_resident ON certificates(resident_id);

-- 回收员收益(每单一行, 含构成)
CREATE TABLE IF NOT EXISTS collector_earnings (
    id             BIGSERIAL PRIMARY KEY,
    collector_id   BIGINT NOT NULL,
    order_id       BIGINT NOT NULL UNIQUE,
    base_cents     BIGINT NOT NULL,
    weight_cents   BIGINT NOT NULL,
    floor_cents    BIGINT NOT NULL,
    distance_cents BIGINT NOT NULL,
    penalty_cents  BIGINT NOT NULL DEFAULT 0, -- 复称差异扣罚
    total_cents    BIGINT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_earnings_collector ON collector_earnings(collector_id, id);

-- 争议(称重/品类/扣杂/价格)
CREATE TABLE IF NOT EXISTS disputes (
    id          BIGSERIAL PRIMARY KEY,
    order_id    BIGINT NOT NULL REFERENCES orders(id),
    item_id     BIGINT,
    raised_by   TEXT NOT NULL,
    kind        TEXT NOT NULL, -- WEIGHT|CATEGORY|CONTAMINATION|PRICE
    detail      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'OPEN', -- OPEN|RESOLVED|REJECTED
    resolution  JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

-- 投诉
CREATE TABLE IF NOT EXISTS complaints (
    id          BIGSERIAL PRIMARY KEY,
    order_id    BIGINT NOT NULL REFERENCES orders(id),
    from_role   TEXT NOT NULL,
    from_id     BIGINT NOT NULL,
    content     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'OPEN', -- OPEN|RESOLVED
    resolution  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

-- 事务性 outbox(与业务写入同事务, relay 负责投递 Kafka)
CREATE TABLE IF NOT EXISTS outbox (
    id           BIGSERIAL PRIMARY KEY,
    topic        TEXT NOT NULL,
    key          TEXT NOT NULL,
    payload      JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox(id) WHERE published_at IS NULL;

-- 消费者幂等记录
CREATE TABLE IF NOT EXISTS processed_events (
    consumer    TEXT NOT NULL,
    event_key   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_key)
);

-- 通知
CREATE TABLE IF NOT EXISTS notifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    kind       TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, id);

-- 街道统计(消费者聚合)
CREATE TABLE IF NOT EXISTS street_stats (
    street      TEXT NOT NULL,
    stat_date   DATE NOT NULL,
    category    TEXT NOT NULL,
    order_count INT NOT NULL DEFAULT 0,
    weight_g    BIGINT NOT NULL DEFAULT 0,
    amount_cents BIGINT NOT NULL DEFAULT 0,
    points      BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (street, stat_date, category)
);
