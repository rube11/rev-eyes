package memory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// WorkspaceEditAction names the edits the workspace may apply to one memory
// the user has identified by id. They mirror the assistant's own tools:
// forget, restore, pin to the profile, unpin, or correct the wording.
type WorkspaceEditAction string

const (
	WorkspaceForget  WorkspaceEditAction = "forget"
	WorkspaceRestore WorkspaceEditAction = "restore"
	WorkspacePin     WorkspaceEditAction = "pin"
	WorkspaceUnpin   WorkspaceEditAction = "unpin"
	WorkspaceUpdate  WorkspaceEditAction = "update"
)

const (
	workspaceTitleRunes   = 120
	workspaceSummaryRunes = 500
)

// The path value comes straight from the client; anything that is not a UUID
// cannot name a memory, so it is reported as missing rather than as a query error.
var memoryIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// WorkspaceEdit is one user-authored change to a memory they can see.
type WorkspaceEdit struct {
	Action  WorkspaceEditAction
	Title   string
	Summary string
}

// Normalize trims the text fields and validates the edit before it reaches
// the database.
func (e WorkspaceEdit) Normalize() (WorkspaceEdit, error) {
	e.Title = strings.Join(strings.Fields(e.Title), " ")
	e.Summary = strings.TrimSpace(e.Summary)
	switch e.Action {
	case WorkspaceForget, WorkspaceRestore, WorkspacePin, WorkspaceUnpin:
		if e.Title != "" || e.Summary != "" {
			return e, ErrInvalidMemoryEdit
		}
		return e, nil
	case WorkspaceUpdate:
		if e.Title == "" || e.Summary == "" {
			return e, ErrInvalidMemoryEdit
		}
		if utf8.RuneCountInString(e.Title) > workspaceTitleRunes ||
			utf8.RuneCountInString(e.Summary) > workspaceSummaryRunes {
			return e, ErrInvalidMemoryEdit
		}
		return e, nil
	default:
		return e, ErrInvalidMemoryEdit
	}
}

// EditByID applies one workspace edit to a memory the user owns. Unlike the
// assistant's lookup-based edits, the id is unambiguous, so no matching is
// needed; the row must still be in the state the action expects.
func (s *Store) EditByID(ctx context.Context, scope tool.Scope, memoryID string, edit WorkspaceEdit) error {
	return editMemoryByID(ctx, s.pool, scope, memoryID, edit)
}

func editMemoryByID(ctx context.Context, database managementDatabase, scope tool.Scope, memoryID string, edit WorkspaceEdit) error {
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.UserID == "" {
		return ErrScopeRequired
	}
	memoryID = strings.TrimSpace(memoryID)
	if !memoryIDPattern.MatchString(memoryID) {
		return ErrMemoryNotFound
	}
	edit, err := edit.Normalize()
	if err != nil {
		return err
	}

	var updated bool
	err = database.QueryRow(
		ctx,
		`with target as (
		     select id
		     from public.memories
		     where id = $2::uuid
		       and user_id = $1::uuid
		       and status = case when $3::text = 'restore' then 'forgotten' else 'active' end
		 ),
		 updated as (
		     update public.memories as memory
		     set status = case $3::text
		             when 'forget' then 'forgotten'
		             when 'restore' then 'active'
		             else memory.status end,
		         inactive_at = case $3::text
		             when 'forget' then statement_timestamp()
		             when 'restore' then null
		             else memory.inactive_at end,
		         profile_override = case $3::text
		             when 'pin' then 'core'
		             when 'unpin' then null
		             else memory.profile_override end,
		         title = case when $3::text = 'update' then $4::text else memory.title end,
		         summary = case when $3::text = 'update' then $5::text else memory.summary end,
		         updated_at = statement_timestamp()
		     from target
		     where memory.id = target.id
		     returning memory.id
		 )
		 select exists(select 1 from updated)`,
		scope.UserID,
		memoryID,
		string(edit.Action),
		edit.Title,
		edit.Summary,
	).Scan(&updated)
	if err != nil {
		return fmt.Errorf("edit memory by id: %w", err)
	}
	if !updated {
		return ErrMemoryNotFound
	}
	return nil
}
