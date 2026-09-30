package harvestpy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const downloadAttempts = 4

var downloadRetryBackoff = 2 * time.Second

func transientDownload(err error) bool {
	var status downloadStatusError
	if errors.As(err, &status) {
		return status.code >= 500 && status.code <= 599
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

func retryDownload(parent, bounded context.Context, timing clock.Clock,
	rawURL, path string, expectedSize int64,
) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse download URL: %w", err)
	}
	for attempt := 0; attempt < downloadAttempts; attempt++ {
		if err := bounded.Err(); err != nil {
			return downloadTimeoutError(parent, bounded, err)
		}
		if attempt > 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove partial download: %w", err)
			}
		}
		requestCtx := bounded
		if attempt > 0 {
			requestCtx = obs.Retry(requestCtx, attempt)
		}
		if attempt < downloadAttempts-1 {
			requestCtx = obs.Presence(requestCtx)
		}
		err := downloadAttempt(requestCtx, bounded, rawURL, path, expectedSize)
		if err == nil {
			return nil
		}
		if bounded.Err() != nil {
			return downloadTimeoutError(parent, bounded, bounded.Err())
		}
		if !transientDownload(err) {
			return err
		}
		if attempt == downloadAttempts-1 {
			return fmt.Errorf("gave up after %d attempts: %w", downloadAttempts, err)
		}
		obs.Logger(obs.Component(bounded, "harvestpy")).Warn("harvestpy.download.retry",
			slog.String("host", parsed.Host), slog.String("path", parsed.Path),
			slog.Int("retries", attempt+1), slog.String(obs.FieldErr, err.Error()))
		if err := timing.Sleep(bounded, downloadRetryBackoff<<attempt); err != nil {
			return downloadTimeoutError(parent, bounded, err)
		}
	}
	panic("unreachable download retry loop")
}
