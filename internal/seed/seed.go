// Package seed 初始化测试数据: 账号/品类/市场价/排班/在岗
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"recycle/internal/store"
)

type account struct {
	account, name, role, token, phone string
}

// 测试账号(token 即 Bearer token)
var Accounts = []account{
	{"resident1", "张阿姨", "resident", "token-resident1", "13800000001"},
	{"resident2", "王大爷", "resident", "token-resident2", "13800000002"},
	{"collector1", "李师傅", "collector", "token-collector1", "13800000011"},
	{"collector2", "赵师傅", "collector", "token-collector2", "13800000012"},
	{"station1", "城东回收站", "station", "token-station1", "13800000021"},
	{"street1", "城东街道办", "street", "token-street1", "13800000031"},
}

func Run(ctx context.Context, st *store.Store, rdb *redis.Client) error {
	return st.InTx(ctx, func(tx pgx.Tx) error {
		// 品类
		cats := []struct {
			code, name string
			ppy, co2   int
		}{
			{"paper", "废纸", 10, 1170},
			{"plastic", "塑料", 10, 1800},
			{"metal", "金属", 10, 2000},
			{"appliance", "小家电", 10, 1500},
			{"clothing", "旧衣物", 10, 3600},
		}
		for _, c := range cats {
			if _, err := tx.Exec(ctx,
				`INSERT INTO categories (code, name, points_per_yuan, co2_g_per_kg)
				 VALUES ($1,$2,$3,$4) ON CONFLICT (code) DO NOTHING`,
				c.code, c.name, c.ppy, c.co2); err != nil {
				return err
			}
		}
		// 市场价(分/kg): 纸1.2元 塑2.0元 金属3.5元 家电1.5元 衣物0.8元
		prices := map[string]int64{"paper": 120, "plastic": 200, "metal": 350, "appliance": 150, "clothing": 80}
		for cat, cents := range prices {
			var n int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM market_prices WHERE category=$1`, cat).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					`INSERT INTO market_prices (category, price_cents_kg, created_by) VALUES ($1,$2,'seed')`,
					cat, cents); err != nil {
					return err
				}
			}
		}
		// 账号
		uid := map[string]int64{}
		for _, a := range Accounts {
			var id int64
			err := tx.QueryRow(ctx,
				`INSERT INTO users (account, name, role, token, phone) VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (account) DO UPDATE SET name=EXCLUDED.name
				 RETURNING id`, a.account, a.name, a.role, a.token, a.phone).Scan(&id)
			if err != nil {
				return err
			}
			uid[a.account] = id
		}
		// 居民档案(城东街道, 王大爷为老人)
		residents := []struct {
			acct, street, addr string
			lat, lng           float64
			floor              int
			elevator, elderly  bool
		}{
			{"resident1", "城东街道", "幸福里小区 3 栋 502", 31.2304, 121.4737, 5, false, false},
			{"resident2", "城东街道", "幸福里小区 7 栋 301", 31.2310, 121.4745, 3, true, true},
		}
		for _, r := range residents {
			if _, err := tx.Exec(ctx,
				`INSERT INTO residents (user_id, street, address, lat, lng, floor, has_elevator, is_elderly)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				 ON CONFLICT (user_id) DO UPDATE SET street=EXCLUDED.street`,
				uid[r.acct], r.street, r.addr, r.lat, r.lng, r.floor, r.elevator, r.elderly); err != nil {
				return err
			}
		}
		// 站点
		var stationID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO stations (user_id, name, street, lat, lng)
			 VALUES ($1,'城东回收站','城东街道',31.2320,121.4800)
			 ON CONFLICT DO NOTHING RETURNING id`, uid["station1"]).Scan(&stationID); err != nil {
			if err == pgx.ErrNoRows {
				if err := tx.QueryRow(ctx, `SELECT id FROM stations WHERE user_id=$1`, uid["station1"]).Scan(&stationID); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		// 回收员
		collectors := []struct {
			acct     string
			lat, lng float64
		}{
			{"collector1", 31.2300, 121.4740},
			{"collector2", 31.2315, 121.4760},
		}
		for _, c := range collectors {
			if _, err := tx.Exec(ctx,
				`INSERT INTO collectors (user_id, station_id, on_duty, lat, lng, max_concurrent)
				 VALUES ($1,$2,true,$3,$4,6)
				 ON CONFLICT (user_id) DO UPDATE SET station_id=EXCLUDED.station_id`,
				uid[c.acct], stationID, c.lat, c.lng); err != nil {
				return err
			}
			// 排班: 今天与明天 0-24 点, 容量 20
			for _, day := range []string{
				time.Now().Format("2006-01-02"),
				time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			} {
				if _, err := tx.Exec(ctx,
					`INSERT INTO schedules (collector_id, work_date, start_hour, end_hour, capacity)
					 VALUES ($1,$2,0,24,20)
					 ON CONFLICT (collector_id, work_date, start_hour) DO NOTHING`,
					uid[c.acct], day); err != nil {
					return err
				}
			}
		}
		// 不可回收物(有害垃圾/危废)处理点
		points := []struct {
			name, street, addr, accepts, hours, phone string
			lat, lng                                  float64
		}{
			{"城东街道有害垃圾归集中心", "城东街道", "城东路 88 号环保大院",
				`["paint_bucket","used_oil_drum","lead_battery","fluorescent_lamp","pesticide_container","expired_medicine"]`,
				"周一至周日 08:00-17:00", "021-58001100", 31.2335, 121.4820},
			{"幸福里小区有害垃圾收集点", "城东街道", "幸福里小区南门物业旁",
				`["paint_bucket","fluorescent_lamp","expired_medicine"]`,
				"全天投放, 每周四清运", "021-58002200", 31.2308, 121.4741},
			{"绿源再生资源危废中转站", "城东街道", "工业园 3 号路 12 号",
				`["paint_bucket","used_oil_drum","lead_battery","pesticide_container"]`,
				"周一至周六 09:00-16:00", "021-58003300", 31.2360, 121.4860},
			{"康泰药店过期药品回收箱", "城东街道", "幸福里商业街 12 号",
				`["expired_medicine"]`,
				"08:30-21:30", "021-58004400", 31.2312, 121.4750},
		}
		for _, p := range points {
			var cnt int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM hazmat_disposal_points WHERE name=$1`, p.name).Scan(&cnt); err != nil {
				return err
			}
			if cnt > 0 {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO hazmat_disposal_points (name, street, address, lat, lng, accepts, open_hours, phone)
				 VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`,
				p.name, p.street, p.addr, p.lat, p.lng, p.accepts, p.hours, p.phone); err != nil {
				return err
			}
		}
		return nil
	})
}

// SyncGeo 回收员 GEO 索引写入 Redis
func SyncGeo(ctx context.Context, st *store.Store, rdb *redis.Client) error {
	rows, err := st.Pool.Query(ctx, `SELECT user_id, lat, lng FROM collectors WHERE on_duty`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var lat, lng float64
		if err := rows.Scan(&id, &lat, &lng); err != nil {
			return err
		}
		if err := rdb.GeoAdd(ctx, "geo:collectors", &redis.GeoLocation{
			Name: fmt.Sprintf("%d", id), Longitude: lng, Latitude: lat,
		}).Err(); err != nil {
			return err
		}
	}
	return rows.Err()
}
