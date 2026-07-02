package cli

import (
	"context"
	"log/slog"
	"time"
)

// refreshTokenPurger is the slice of the store the reaper needs: delete refresh
// tokens that expired at or before cutoff (unix seconds), returning how many
// were removed. *store.Store satisfies it.
type refreshTokenPurger interface {
	PurgeExpiredRefreshTokens(ctx context.Context, cutoff int64) (int64, error)
}

// runRefreshTokenReaper prunes expired refresh tokens on a fixed interval until
// ctx is cancelled, calling purger directly (the same work POST
// /admin/refresh-tokens/purge does, without going through the API). It runs an
// immediate sweep at startup, then one every interval; on ctx cancellation
// (graceful shutdown) it returns promptly without a final sweep. now supplies
// the cutoff so the reaper stays consistent with token issuance and with the
// clock injected in tests; a non-positive interval disables the reaper.
//
// It is meant to run in its own goroutine and logs rather than returns errors,
// since a transient purge failure should not stop future sweeps.
func runRefreshTokenReaper(ctx context.Context, purger refreshTokenPurger, interval time.Duration, now func() time.Time, logger *slog.Logger) {
	if interval <= 0 {
		return
	}

	sweep := func() {
		purged, err := purger.PurgeExpiredRefreshTokens(ctx, now().Unix())
		switch {
		case err != nil:
			// ctx cancellation cancels the in-flight purge too; that is expected
			// shutdown noise, so only report genuine failures.
			if ctx.Err() == nil {
				logger.Error("refresh-token reaper sweep failed", "err", err)
			}
		case purged > 0:
			logger.Info("refresh-token reaper purged expired tokens", "purged", purged)
		}
	}

	sweep()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
