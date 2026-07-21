package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// LoadConversation returns transcript entries after the saved summary and
// before the current user utterance.
func (s *Store) LoadConversation(
	ctx context.Context,
	scope tool.Scope,
	currentUtteranceID string,
) (Conversation, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	currentUtteranceID = strings.TrimSpace(currentUtteranceID)
	if scope.UserID == "" || scope.SessionID == "" || currentUtteranceID == "" {
		return Conversation{}, ErrScopeRequired
	}

	rows, err := s.pool.Query(
		ctx,
		`with bounds as (
		     select
		         coalesce(s.context_summary, '') as summary,
		         cutoff.started_at as cutoff_at,
		         cutoff.id as cutoff_id,
		         current.started_at as current_at,
		         current.id as current_id
		     from public.sessions s
		     join public.transcript_utterances current
		       on current.id = $3::uuid
		      and current.session_id = s.id
		      and current.user_id = s.user_id
		     left join public.transcript_utterances cutoff
		       on cutoff.id = s.context_summary_through_id
		      and cutoff.session_id = s.id
		      and cutoff.user_id = s.user_id
		     where s.id = $2::uuid
		       and s.user_id = $1::uuid
		       and s.status = 'active'
		 )
		 select b.summary, t.id::text, t.speaker, t.text
		 from bounds b
		 left join public.transcript_utterances t
		   on t.session_id = $2::uuid
		  and t.user_id = $1::uuid
		  and t.speaker in ('user', 'assistant')
		  and (
		      b.cutoff_id is null
		      or (t.started_at, t.id) > (b.cutoff_at, b.cutoff_id)
		  )
		  and (t.started_at, t.id) < (b.current_at, b.current_id)
		 order by t.started_at, t.id`,
		scope.UserID,
		scope.SessionID,
		currentUtteranceID,
	)
	if err != nil {
		return Conversation{}, fmt.Errorf("load conversation: %w", err)
	}
	defer rows.Close()

	var conversation Conversation
	found := false
	for rows.Next() {
		found = true
		var id, speaker, text pgtype.Text
		if err := rows.Scan(&conversation.Summary, &id, &speaker, &text); err != nil {
			return Conversation{}, fmt.Errorf("scan conversation: %w", err)
		}
		if !id.Valid {
			continue
		}
		conversation.Messages = append(conversation.Messages, Message{
			ID:      id.String,
			Speaker: Speaker(speaker.String),
			Text:    text.String,
		})
	}
	if err := rows.Err(); err != nil {
		return Conversation{}, fmt.Errorf("read conversation: %w", err)
	}
	if !found {
		return Conversation{}, ErrSessionUnavailable
	}
	return conversation, nil
}

// SaveConversationSummary advances the transcript cutoff for one active
// session. The cutoff must belong to the trusted user and session scope.
func (s *Store) SaveConversationSummary(
	ctx context.Context,
	scope tool.Scope,
	throughUtteranceID string,
	summary string,
) error {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	throughUtteranceID = strings.TrimSpace(throughUtteranceID)
	summary = strings.TrimSpace(summary)
	if scope.UserID == "" || scope.SessionID == "" || throughUtteranceID == "" {
		return ErrScopeRequired
	}
	if summary == "" {
		return ErrTextRequired
	}

	result, err := s.pool.Exec(
		ctx,
		`update public.sessions s
		 set context_summary = $4,
		     context_summary_through_id = $3::uuid
		 where s.id = $2::uuid
		   and s.user_id = $1::uuid
		   and s.status = 'active'
		   and exists (
		       select 1
		       from public.transcript_utterances t
		       where t.id = $3::uuid
		         and t.session_id = s.id
		         and t.user_id = s.user_id
		   )`,
		scope.UserID,
		scope.SessionID,
		throughUtteranceID,
		summary,
	)
	if err != nil {
		return fmt.Errorf("save conversation summary: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSessionUnavailable
	}
	return nil
}
