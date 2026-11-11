package rotation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// AdvanceAfterCheckout 在收银成功后推进这张单上的服务员工：点客按设置决定占轮，整轮或半轮按设置排到队尾。
func AdvanceAfterCheckout(ctx context.Context, env ops.Env, orderID uuid.UUID) error {
	rows, err := env.Q.OrderServiceStaff(ctx, dbgen.OrderServiceStaffParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	staffIDs := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		if id != nil {
			staffIDs[*id] = true
		}
	}
	if len(staffIDs) == 0 {
		return nil
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return err
	}
	items, err := env.Q.OrderServiceItems(ctx, dbgen.OrderServiceItemsParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	byAppointment := false
	if rules.RotationHalfRule() == "by_appointment" {
		byAppointment, err = booking.OrderHasCompletedAppointment(ctx, env.Q, env.Caller.StoreID, orderID)
		if err != nil {
			return err
		}
	}
	half, err := halfTurn(items, rules, byAppointment)
	if err != nil {
		return err
	}
	_, err = env.Q.LockAllRotationQueues(ctx, env.Caller.StoreID)
	if err != nil {
		return err
	}
	serving, err := env.Q.LockServingRotationTurnsByOrder(ctx, dbgen.LockServingRotationTurnsByOrderParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	type turnDay struct {
		queueID uuid.UUID
		day     time.Time
	}
	order := []turnDay{}
	targets := map[turnDay][]uuid.UUID{}
	seen := map[turnDay]map[uuid.UUID]bool{}
	for _, row := range serving {
		if !staffIDs[row.StaffID] {
			continue
		}
		key := turnDay{queueID: row.QueueID, day: row.BusinessDate.Time}
		if seen[key] == nil {
			seen[key] = map[uuid.UUID]bool{}
			order = append(order, key)
		}
		if seen[key][row.StaffID] {
			continue
		}
		seen[key][row.StaffID] = true
		targets[key] = append(targets[key], row.StaffID)
	}
	for _, key := range order {
		turns, err := loadTurns(ctx, env, key.queueID, key.day)
		if err != nil {
			return err
		}
		changed := false
		for _, staffID := range targets[key] {
			index := indexOf(turns, staffID)
			if index < 0 {
				continue
			}
			turn := &turns[index]
			if turn.turn.Status != StatusServing || turn.turn.ServingOrderID == nil || *turn.turn.ServingOrderID != orderID {
				continue
			}
			from := turn.turn.Position
			to := from
			designated := turn.turn.ServingDesignated
			switch {
			case designated && rules.RotationDesignatedTakesTurn() == "no":
				setWaiting(turn)
				clearServing(turn)
				if err := recordEvent(ctx, env, key.queueID, key.day, &staffID, "advance_designated_no_turn", nil, nil, position(from), position(to), nil, &orderID, &designated); err != nil {
					return err
				}
			case half:
				if turn.turn.HalfPending {
					from, to, _ = moveToTail(turns, staffID)
					turn = &turns[indexOf(turns, staffID)]
					turn.turn.HalfPending = false
				} else {
					turn.turn.HalfPending = true
				}
				setWaiting(turn)
				clearServing(turn)
				if err := recordEvent(ctx, env, key.queueID, key.day, &staffID, "advance_half", nil, nil, position(from), position(to), nil, &orderID, &designated); err != nil {
					return err
				}
			default:
				from, to, _ = moveToTail(turns, staffID)
				turn = &turns[indexOf(turns, staffID)]
				turn.turn.HalfPending = false
				setWaiting(turn)
				clearServing(turn)
				if err := recordEvent(ctx, env, key.queueID, key.day, &staffID, "advance_whole", nil, nil, position(from), position(to), nil, &orderID, &designated); err != nil {
					return err
				}
			}
			changed = true
		}
		if changed {
			if err := persistTurns(ctx, env, key.queueID, key.day, turns, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func halfTurn(items []dbgen.OrderServiceItemsRow, rules settings.Rules, byAppointment bool) (bool, error) {
	switch rules.RotationHalfRule() {
	case "by_price":
		threshold := rules.RotationHalfPriceBelow()
		if threshold <= 0 {
			return false, nil
		}
		net := money.Amount{}
		for _, item := range items {
			line, err := money.Amount{}.Apply(item.Price.Sub(item.Discount))
			if err != nil {
				return false, err
			}
			if net, err = net.Add(line); err != nil {
				return false, err
			}
		}
		var limit money.Amount
		if err := limit.Scan(fmt.Sprintf("%d", threshold)); err != nil {
			return false, err
		}
		return net.Cmp(limit) < 0, nil
	case "by_item":
		if len(items) == 0 {
			return false, nil
		}
		for _, item := range items {
			if !item.HalfItem {
				return false, nil
			}
		}
		return true, nil
	case "by_appointment":
		return byAppointment, nil
	default:
		return false, nil
	}
}
