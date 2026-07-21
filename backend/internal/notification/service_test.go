package notification

import (
	"context"
	"strconv"
	"testing"
)

type memoryRepository struct {
	pending []Notification
	nextID  int
}

func (r *memoryRepository) Create(
	_ context.Context,
	_ string,
	text string,
) (Notification, error) {
	r.nextID++
	notification := Notification{ID: strconv.Itoa(r.nextID), Text: text}
	r.pending = append(r.pending, notification)
	return notification, nil
}

func (r *memoryRepository) Pending(context.Context, string) ([]Notification, error) {
	return append([]Notification(nil), r.pending...), nil
}

func (r *memoryRepository) MarkDelivered(
	_ context.Context,
	_ string,
	notificationID string,
) error {
	for index, notification := range r.pending {
		if notification.ID == notificationID {
			r.pending = append(r.pending[:index], r.pending[index+1:]...)
			break
		}
	}
	return nil
}

type memorySender struct {
	online bool
	sent   []string
}

func (s *memorySender) Send(_ string, text string) bool {
	if !s.online {
		return false
	}
	s.sent = append(s.sent, text)
	return true
}

func TestServiceDeliversPendingNotificationOnReconnect(t *testing.T) {
	repository := &memoryRepository{}
	sender := &memorySender{}
	service, err := NewService(repository, sender)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := service.Notify(context.Background(), "user", "Time to leave"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(repository.pending) != 1 || len(sender.sent) != 0 {
		t.Fatalf("offline state = pending %d, sent %d", len(repository.pending), len(sender.sent))
	}

	sender.online = true
	if err := service.Flush(context.Background(), "user"); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(repository.pending) != 0 || len(sender.sent) != 1 || sender.sent[0] != "Time to leave" {
		t.Fatalf("online state = pending %d, sent %v", len(repository.pending), sender.sent)
	}
}

func TestServiceMarksImmediateDelivery(t *testing.T) {
	repository := &memoryRepository{}
	sender := &memorySender{online: true}
	service, err := NewService(repository, sender)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := service.Notify(context.Background(), "user", "Take a break"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(repository.pending) != 0 || len(sender.sent) != 1 {
		t.Fatalf("state = pending %d, sent %v", len(repository.pending), sender.sent)
	}
}
