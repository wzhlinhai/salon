package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
)

func (p *Checkout) checkRedPacket(ctx context.Context, payment Input, customerID uuid.UUID) error {
	packet := p.packets[*payment.RedPacketID]
	if err := redpackets.CheckUsable(ctx, p.env, packet, customerID, p.day); err != nil {
		return err
	}
	if payment.Amount.Cmp(packet.FaceValue) > 0 {
		return ops.Rule("红包抵扣不能超过面额")
	}
	return nil
}
func (p *Checkout) recordRedPacket(ctx context.Context, payment Input, paymentID uuid.UUID) error {
	return redpackets.MarkUsed(ctx, p.env, p.packets[*payment.RedPacketID], paymentID, p.day)
}
func reverseRedPacket(ctx context.Context, env ops.Env, packet dbgen.RedPacket, day time.Time) error {
	return redpackets.Restore(ctx, env, packet, day)
}
