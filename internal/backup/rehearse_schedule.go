package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// runScheduledRehearsal runs under tick's cluster lease. It reads only an
// existing encrypted archive and invokes the same isolated scratch restore as
// the manual CLI. Persisting admission before I/O bounds crash/retry loops.
func (s *Service) runScheduledRehearsal(ctx context.Context, logger *slog.Logger, now time.Time) {
	if s.opt.RehearseCron == "" || s.opt.RehearseURL == "" || !s.Enabled() {
		return
	}
	last, retry := s.readStatus().RestoreTested, s.rehearsalRetryAt
	if s.db != nil {
		var success, retryAt, attempt sql.NullTime
		if err := s.db.QueryRowContext(ctx, `SELECT rehearsal_last_success, rehearsal_retry_at, rehearsal_attempt_at FROM backup_scheduler_state WHERE id=1`).Scan(&success, &retryAt, &attempt); err != nil {
			logger.Error("rehearsal schedule read failed", "err", err)
			return
		}
		if success.Valid {
			last = success.Time
		}
		if retryAt.Valid {
			retry = retryAt.Time
		}
		if attempt.Valid {
			if err := s.recordRehearsalFailure(); err != nil {
				logger.Error("rehearsal interruption status failed", "err", err)
			}
		}
	}
	if !cronDue(s.opt.RehearseCron, last, now) || now.Before(retry) {
		return
	}
	s.rehearsalRetryAt = now.Add(statusRetryBackoff)
	if s.db != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE backup_scheduler_state SET rehearsal_attempt_at=$1, rehearsal_retry_at=$2, updated_at=now() WHERE id=1`, now, s.rehearsalRetryAt); err != nil {
			logger.Error("rehearsal admission failed", "err", err)
			return
		}
	}
	// Work follows scheduler shutdown/cancellation. Only the short status
	// settlement below is detached, so an interrupted attempt remains visible.
	runCtx, cancel := context.WithTimeout(ctx, rehearsalTimeout)
	defer cancel()
	err := s.rehearseNewest(runCtx)
	settle, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err == nil {
		if s.db != nil {
			_, err = s.db.ExecContext(settle, `UPDATE backup_scheduler_state SET rehearsal_last_success=$1, rehearsal_attempt_at=NULL, rehearsal_retry_at=NULL, updated_at=now() WHERE id=1`, time.Now())
		}
	} else {
		err = errors.Join(err, s.recordRehearsalFailure())
		if s.db != nil {
			_, stateErr := s.db.ExecContext(settle, `UPDATE backup_scheduler_state SET rehearsal_attempt_at=NULL, rehearsal_retry_at=$1, updated_at=now() WHERE id=1`, time.Now().Add(statusRetryBackoff))
			err = errors.Join(err, stateErr)
		}
	}
	if err != nil {
		logger.Error("scheduled restore rehearsal failed", "err", err)
		return
	}
	logger.Info("scheduled restore rehearsal succeeded")
}

// rehearseNewest holds the memory lease across archive loading and restoring.
func (s *Service) rehearseNewest(ctx context.Context) error {
	ctx, release, err := s.AcquireWork(ctx)
	if err != nil {
		return err
	}
	defer release()
	files, err := s.List()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no local backup available for rehearsal")
	}
	enc, err := s.OpenContext(ctx, files[0].Name)
	if err != nil {
		return fmt.Errorf("open rehearsal backup: %w", err)
	}
	_, err = s.RehearseRestore(ctx, enc)
	return err
}

// recordRehearsalFailure never exposes database URLs or restore stderr to HTML.
func (s *Service) recordRehearsalFailure() error {
	return s.updateStatus(func(st *Status) {
		ok := false
		st.RehearsalOK = &ok
		st.RehearsalNote = "Restore-Probe fehlgeschlagen oder unterbrochen. Serverprotokoll prüfen."
	})
}
