package domain

import "fmt"

// 状态机: 允许的流转 (from -> []to)
var transitions = map[string][]string{
	StatusPending:          {StatusAssigned, StatusCancelled},
	StatusAssigned:         {StatusAccepted, StatusCancelled, StatusPending}, // 回退 PENDING = 重新派单
	StatusAccepted:         {StatusEnRoute, StatusCancelled},
	StatusEnRoute:          {StatusArrived, StatusCancelled},
	StatusArrived:          {StatusResidentVerified, StatusCancelled},
	StatusResidentVerified: {StatusWeighed},
	StatusWeighed:          {StatusConfirmed, StatusDisputed, StatusResidentVerified}, // 回退 = 重新称重(含临时加品类后)
	StatusDisputed:         {StatusWeighed, StatusConfirmed, StatusCancelled},         // 争议裁决
	StatusConfirmed:        {StatusSettled},
	StatusSettled:          {StatusDelivered},
	StatusDelivered:        {StatusInboundConfirmed},
	StatusInboundConfirmed: {StatusCompleted},
}

// CanTransit 判断 from -> to 是否合法
func CanTransit(from, to string) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// MustTransit 校验流转, 非法时返回错误
func MustTransit(from, to string) error {
	if !CanTransit(from, to) {
		return fmt.Errorf("illegal order transition: %s -> %s", from, to)
	}
	return nil
}

// CancellableByResident 居民可取消的状态
func CancellableByResident(status string) bool {
	switch status {
	case StatusPending, StatusAssigned, StatusAccepted, StatusEnRoute, StatusArrived:
		return true
	}
	return false
}

// ActiveForCollector 回收员在途单状态(占用并发额度)
func ActiveForCollector(status string) bool {
	switch status {
	case StatusAssigned, StatusAccepted, StatusEnRoute, StatusArrived,
		StatusResidentVerified, StatusWeighed, StatusDisputed, StatusConfirmed:
		return true
	}
	return false
}
