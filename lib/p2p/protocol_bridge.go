package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
)

func WriteBridgeMessage(c *conn.Conn, flag string, payload any) error {
	if c == nil {
		return fmt.Errorf("nil bridge conn")
	}
	_, err := c.SendInfo(payload, flag)
	return err
}

func WritePunchProgress(c *conn.Conn, progress P2PPunchProgress) error {
	if progress.Timestamp == 0 {
		progress.Timestamp = time.Now().UnixMilli()
	}
	return WriteBridgeMessage(c, common.P2P_PUNCH_PROGRESS, progress)
}

func ReadBridgeJSON[T any](c *conn.Conn, expectedFlag string) (T, error) {
	var zero T
	if c == nil {
		return zero, fmt.Errorf("nil bridge conn")
	}
	flag, err := c.ReadFlag()
	if err != nil {
		return zero, err
	}
	raw, err := c.GetShortLenContent()
	if err != nil {
		return zero, err
	}
	if flag == common.P2P_PUNCH_ABORT {
		var abort P2PPunchAbort
		if err := json.Unmarshal(raw, &abort); err != nil {
			return zero, ErrP2PSessionAbort
		}
		if abort.Reason == "" {
			return zero, ErrP2PSessionAbort
		}
		return zero, fmt.Errorf("%w: %s", ErrP2PSessionAbort, abort.Reason)
	}
	if expectedFlag != "" && flag != expectedFlag {
		return zero, fmt.Errorf("unexpected flag %q", flag)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func ReadBridgeJSONContext[T any](ctx context.Context, c *conn.Conn, expectedFlag string) (T, error) {
	var zero T
	if ctx == nil {
		return ReadBridgeJSON[T](c, expectedFlag)
	}
	if err := ctx.Err(); err != nil {
		return zero, mapP2PContextError(err)
	}
	restoreDeadline := interruptBridgeReadOnContext(ctx, c)
	if restoreDeadline != nil {
		defer restoreDeadline()
	}
	out, err := ReadBridgeJSON[T](c, expectedFlag)
	if err == nil {
		return out, nil
	}
	if isBridgeReadTimeout(err) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, mapP2PContextError(ctxErr)
		}
	}
	return zero, err
}

func interruptBridgeReadOnContext(ctx context.Context, c *conn.Conn) func() {
	if ctx == nil || c == nil {
		return nil
	}
	stop := context.AfterFunc(ctx, func() {
		_ = c.SetReadDeadline(time.Now())
	})
	return func() {
		_ = stop()
		_ = c.SetReadDeadline(time.Time{})
	}
}

func interruptPacketReadOnContext(ctx context.Context, c net.PacketConn) func() {
	if ctx == nil || !packetConnUsable(c) {
		return nil
	}
	stop := context.AfterFunc(ctx, func() {
		_ = c.SetReadDeadline(time.Now())
	})
	return func() {
		_ = stop()
		_ = c.SetReadDeadline(time.Time{})
	}
}

func isBridgeReadTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
