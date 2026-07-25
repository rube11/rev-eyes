package watch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ClaimScheduled loads one active watch only when its expected check is due.
func (s *Store) ClaimScheduled(
	ctx context.Context,
	resourceID string,
) (Watch, bool, error) {
	if _, err := s.pool.Exec(
		ctx,
		`update public.watches
		 set status = 'expired'
		 where id = $1::uuid
		   and status = 'active'
		   and expires_at <= statement_timestamp()`,
		resourceID,
	); err != nil {
		return Watch{}, false, fmt.Errorf("expire scheduled watch: %w", err)
	}

	var claimed Watch
	err := s.pool.QueryRow(
		ctx,
		`select
		     id::text,
		     user_id::text,
		     query,
		     condition,
		     next_check_at
		 from public.watches
		 where id = $1::uuid
		   and status = 'active'
		   and next_check_at <= statement_timestamp()
		   and expires_at > statement_timestamp()`,
		resourceID,
	).Scan(
		&claimed.ID,
		&claimed.UserID,
		&claimed.Query,
		&claimed.Condition,
		&claimed.NextCheckAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Watch{}, false, nil
	}
	if err != nil {
		return Watch{}, false, fmt.Errorf("claim scheduled watch: %w", err)
	}
	return claimed, true, nil
}

// Record atomically stores observed URLs and queues one genuinely new result.
func (s *Store) Record(ctx context.Context, claimed Watch, items []Item) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin watch observation: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		lastCheckedAt *time.Time
		seenURLs      []string
	)
	err = tx.QueryRow(
		ctx,
		`select last_checked_at, seen_urls
		 from public.watches
		 where id = $1::uuid
		   and user_id = $2::uuid
		   and status = 'active'
		   and next_check_at = $3
		 for update`,
		claimed.ID,
		claimed.UserID,
		claimed.NextCheckAt.UTC(),
	).Scan(&lastCheckedAt, &seenURLs)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load watch observation: %w", err)
	}

	seen := make(map[string]struct{}, len(seenURLs)+len(items))
	for _, seenURL := range seenURLs {
		seen[seenURL] = struct{}{}
	}
	var firstNew *Item
	for _, item := range items {
		item.Title = strings.TrimSpace(item.Title)
		item.URL = strings.TrimSpace(item.URL)
		if item.Title == "" || item.URL == "" {
			continue
		}
		if _, exists := seen[item.URL]; exists {
			continue
		}
		seen[item.URL] = struct{}{}
		seenURLs = append(seenURLs, item.URL)
		if firstNew == nil {
			copy := item
			firstNew = &copy
		}
	}

	if _, err := tx.Exec(
		ctx,
		`update public.watches
		 set last_checked_at = statement_timestamp(),
		     seen_urls = $4,
		     next_check_at = least(
		         expires_at,
		         next_check_at + make_interval(mins => interval_minutes)
		     )
		 where id = $1::uuid
		   and user_id = $2::uuid
		   and next_check_at = $3`,
		claimed.ID,
		claimed.UserID,
		claimed.NextCheckAt.UTC(),
		seenURLs,
	); err != nil {
		return false, fmt.Errorf("save watch observation: %w", err)
	}

	notify := lastCheckedAt != nil && firstNew != nil
	if notify {
		if _, err := tx.Exec(
			ctx,
			`insert into public.notifications (user_id, text)
			 values ($1::uuid, $2)`,
			claimed.UserID,
			notificationText(claimed.Condition, *firstNew),
		); err != nil {
			return false, fmt.Errorf("enqueue watch notification: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit watch observation: %w", err)
	}
	return notify, nil
}

func notificationText(condition string, item Item) string {
	condition = strings.TrimSpace(condition)
	title := strings.TrimSpace(item.Title)
	if utf8.RuneCountInString(title) > 300 {
		title = string([]rune(title)[:300]) + "…"
	}
	source := "the web"
	if parsed, err := url.Parse(item.URL); err == nil && parsed.Hostname() != "" {
		source = parsed.Hostname()
	}
	return "Possible update on " + condition + ": " + title + " (" + source + ")"
}
