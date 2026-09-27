// 积分账本集成测试: 需要 PG_DSN 指向测试库(默认 5433/recycle)
// 运行: go test ./internal/store/ -run Points -v
package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@127.0.0.1:5433/recycle?sslmode=disable"
	}
	st, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := st.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// TestCreditPointsIdempotent 同一幂等键重复入账只生效一次, 余额正确
func TestCreditPointsIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	resident := int64(99990001)
	key := "test:settlement:" + fmt.Sprint(resident)

	// 造一个真实居民(FK 约束)并清理旧积分数据
	st.Pool.Exec(ctx, `DELETE FROM points_entries WHERE resident_id=$1`, resident)
	st.Pool.Exec(ctx, `DELETE FROM points_accounts WHERE resident_id=$1`, resident)
	st.Pool.Exec(ctx, `DELETE FROM residents WHERE user_id=$1`, resident)
	st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, resident)
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO users (id, account, name, role, token) VALUES ($1,$2,'测试','resident',$3)`,
		resident, fmt.Sprintf("test-resident-%d", resident), fmt.Sprintf("test-token-%d", resident)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO residents (user_id, street, address, lat, lng) VALUES ($1,'测试街道','测试地址',31.0,121.0)`,
		resident); err != nil {
		t.Fatal(err)
	}
	defer func() {
		st.Pool.Exec(ctx, `DELETE FROM points_entries WHERE resident_id=$1`, resident)
		st.Pool.Exec(ctx, `DELETE FROM points_accounts WHERE resident_id=$1`, resident)
		st.Pool.Exec(ctx, `DELETE FROM residents WHERE user_id=$1`, resident)
		st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, resident)
	}()

	err := st.InTx(ctx, func(tx pgx.Tx) error {
		_, created, err := CreditPoints(ctx, tx, resident, nil, nil, 100, "RECYCLE_CREDIT", key)
		if err != nil || !created {
			return fmt.Errorf("first credit: created=%v err=%v", created, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 重放: 同幂等键不得再次入账
	err = st.InTx(ctx, func(tx pgx.Tx) error {
		_, created, err := CreditPoints(ctx, tx, resident, nil, nil, 100, "RECYCLE_CREDIT", key)
		if err != nil {
			return err
		}
		if created {
			return fmt.Errorf("replay should not create new entry")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bal, err := PointsBalance(ctx, st.Pool, resident)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 100 {
		t.Fatalf("balance=%d, want 100 (idempotent)", bal)
	}
}
