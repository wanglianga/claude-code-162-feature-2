// Package svc 实现回收服务的核心业务逻辑
package svc

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"recycle/internal/config"
	"recycle/internal/domain"
	"recycle/internal/store"
)

type Service struct {
	Store *store.Store
	Redis *redis.Client
	Cfg   config.Config
}

func New(st *store.Store, rdb *redis.Client, cfg config.Config) *Service {
	return &Service{Store: st, Redis: rdb, Cfg: cfg}
}

// ApiError 带 HTTP 状态码的业务错误
type ApiError struct {
	Code    int
	Message string
}

func (e *ApiError) Error() string { return e.Message }

func badReq(format string, args ...any) *ApiError {
	return &ApiError{Code: 400, Message: fmt.Sprintf(format, args...)}
}
func notFound(what string) *ApiError {
	return &ApiError{Code: 404, Message: what + " not found"}
}
func conflict(format string, args ...any) *ApiError {
	return &ApiError{Code: 409, Message: fmt.Sprintf(format, args...)}
}
func forbidden(format string, args ...any) *ApiError {
	return &ApiError{Code: 403, Message: fmt.Sprintf(format, args...)}
}

// transit 在事务内完成: 状态机校验 -> 乐观锁推进 -> 事件落库 -> outbox
// sets 为随状态一起更新的字段(列名白名单由调用方硬编码)
func (s *Service) transit(ctx context.Context, tx pgx.Tx, o *domain.Order, to string,
	actorRole string, actorID int64, detail map[string]any, sets map[string]any) error {

	if err := domain.MustTransit(o.Status, to); err != nil {
		return conflict("order %s cannot transit %s -> %s", o.OrderNo, o.Status, to)
	}
	if sets == nil {
		if err := store.UpdateOrderStatus(ctx, tx, o.ID, o.Version, to); err != nil {
			return err
		}
	} else {
		if err := store.UpdateOrderFields(ctx, tx, o.ID, o.Version, to, sets); err != nil {
			return err
		}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	ev := &domain.OrderEvent{
		OrderID: o.ID, FromStatus: o.Status, ToStatus: to,
		ActorRole: actorRole, ActorID: actorID, Detail: detail,
	}
	if err := store.AddOrderEvent(ctx, tx, ev); err != nil {
		return err
	}
	// outbox: 与状态变更同事务, relay 投递 Kafka
	if err := store.AddOutbox(ctx, tx, domain.TopicOrderEvents, fmt.Sprintf("order:%d", o.ID), map[string]any{
		"event_id": ev.ID, "order_id": o.ID, "order_no": o.OrderNo,
		"from": o.Status, "to": to, "actor_role": actorRole, "actor_id": actorID,
		"resident_id": o.ResidentID, "collector_id": o.CollectorID, "station_id": o.StationID,
		"street": o.Street, "detail": detail, "at": time.Now().UTC(),
	}); err != nil {
		return err
	}
	// 更新内存态
	from := o.Status
	o.Status = to
	o.Version++
	_ = from
	return nil
}

// notify 事务内写入通知 outbox
func (s *Service) notify(ctx context.Context, tx pgx.Tx, userID int64, kind, content string) error {
	return store.AddOutbox(ctx, tx, domain.TopicNotifications, fmt.Sprintf("user:%d", userID), map[string]any{
		"user_id": userID, "kind": kind, "content": content, "at": time.Now().UTC(),
	})
}

func randDigits(n int) string {
	out := make([]byte, n)
	for i := range out {
		v, _ := rand.Int(rand.Reader, big.NewInt(10))
		out[i] = byte('0' + v.Int64())
	}
	return string(out)
}

func orderNo() string {
	v, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("RC%s-%06d", time.Now().Format("20060102"), v.Int64())
}
