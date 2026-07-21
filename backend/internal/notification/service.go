package notification

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrRepositoryRequired = errors.New("notification repository is required")
	ErrSenderRequired     = errors.New("notification sender is required")
)

type Repository interface {
	Create(context.Context, string, string) (Notification, error)
	Pending(context.Context, string) ([]Notification, error)
	MarkDelivered(context.Context, string, string) error
}

type Sender interface {
	Send(userID, text string) bool
}

type Service struct {
	repository Repository
	sender     Sender
}

func NewService(repository Repository, sender Sender) (*Service, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if sender == nil {
		return nil, ErrSenderRequired
	}
	return &Service{repository: repository, sender: sender}, nil
}

// Notify persists a message before attempting immediate delivery.
func (s *Service) Notify(ctx context.Context, userID, text string) error {
	notification, err := s.repository.Create(ctx, userID, text)
	if err != nil {
		return err
	}
	if !s.sender.Send(userID, notification.Text) {
		return nil
	}
	if err := s.repository.MarkDelivered(ctx, userID, notification.ID); err != nil {
		return fmt.Errorf("record notification delivery: %w", err)
	}
	return nil
}

// Flush delivers pending messages in order until the user disconnects.
func (s *Service) Flush(ctx context.Context, userID string) error {
	notifications, err := s.repository.Pending(ctx, userID)
	if err != nil {
		return err
	}
	for _, notification := range notifications {
		if !s.sender.Send(userID, notification.Text) {
			return nil
		}
		if err := s.repository.MarkDelivered(ctx, userID, notification.ID); err != nil {
			return fmt.Errorf("record notification delivery: %w", err)
		}
	}
	return nil
}
