package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

const maxTextLength = 1000

var (
	ErrDatabaseRequired       = errors.New("notification database is required")
	ErrUserIDRequired         = errors.New("notification user ID is required")
	ErrTextInvalid            = errors.New("notification text must contain 1 to 1000 characters")
	ErrNotificationIDRequired = errors.New("notification ID is required")
)

type Notification struct {
	ID   string
	Text string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Create(ctx context.Context, userID, text string) (Notification, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Notification{}, ErrUserIDRequired
	}
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxTextLength {
		return Notification{}, ErrTextInvalid
	}

	notification := Notification{Text: text}
	err := s.pool.QueryRow(
		ctx,
		`insert into public.notifications (user_id, text)
		 values ($1::uuid, $2)
		 returning id::text`,
		userID,
		text,
	).Scan(&notification.ID)
	if err != nil {
		return Notification{}, fmt.Errorf("create notification: %w", err)
	}
	return notification, nil
}

func (s *Store) Pending(ctx context.Context, userID string) ([]Notification, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserIDRequired
	}

	rows, err := s.pool.Query(
		ctx,
		`select id::text, text
		 from public.notifications
		 where user_id = $1::uuid
		   and delivered_at is null
		 order by created_at, id`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var notification Notification
		if err := rows.Scan(&notification.ID, &notification.Text); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read notifications: %w", err)
	}
	return notifications, nil
}

func (s *Store) MarkDelivered(ctx context.Context, userID, notificationID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrUserIDRequired
	}
	notificationID = strings.TrimSpace(notificationID)
	if notificationID == "" {
		return ErrNotificationIDRequired
	}

	_, err := s.pool.Exec(
		ctx,
		`update public.notifications
		 set delivered_at = coalesce(delivered_at, statement_timestamp())
		 where id = $2::uuid
		   and user_id = $1::uuid`,
		userID,
		notificationID,
	)
	if err != nil {
		return fmt.Errorf("mark notification delivered: %w", err)
	}
	return nil
}
