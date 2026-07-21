package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const searchLimit = 5

var (
	ErrDatabaseRequired  = errors.New("memory database is required")
	ErrScopeRequired     = errors.New("memory scope is required")
	ErrSourceRequired    = errors.New("source utterance ID is required")
	ErrSourceUnavailable = errors.New("source utterance is unavailable")
)

// Store persists confirmed memories and their transcript sources.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

// Remember saves one confirmed card linked to its trusted source utterance.
func (s *Store) Remember(
	ctx context.Context,
	scope tool.Scope,
	sourceUtteranceID string,
	card Card,
) error {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return ErrScopeRequired
	}
	sourceUtteranceID = strings.TrimSpace(sourceUtteranceID)
	if sourceUtteranceID == "" {
		return ErrSourceRequired
	}
	card = card.Normalize()
	if err := card.Validate(); err != nil {
		return err
	}

	details, err := json.Marshal(card.Details)
	if err != nil {
		return fmt.Errorf("encode memory details: %w", err)
	}
	entities, err := json.Marshal(card.Entities)
	if err != nil {
		return fmt.Errorf("encode memory entities: %w", err)
	}
	topics := make([]string, len(card.Topics))
	for index, topic := range card.Topics {
		topics[index] = string(topic)
	}

	var memoryID string
	err = s.pool.QueryRow(
		ctx,
		`with source as (
		     select id, user_id
		     from public.transcript_utterances
		     where id = $3::uuid
		       and user_id = $1::uuid
		       and session_id = $2::uuid
		 ),
		 created_memory as (
		     insert into public.memories (
		         user_id,
		         topics,
		         kind,
		         title,
		         summary,
		         details,
		         entities
		     )
		     select
		         user_id,
		         $4,
		         $5,
		         $6,
		         $7,
		         $8::jsonb,
		         $9::jsonb
		     from source
		     returning id, user_id
		 )
		 insert into public.memory_sources (
		     user_id,
		     memory_id,
		     utterance_id
		 )
		 select
		     created_memory.user_id,
		     created_memory.id,
		     source.id
		 from created_memory
		 join source using (user_id)
		 returning memory_id::text`,
		scope.UserID,
		scope.SessionID,
		sourceUtteranceID,
		topics,
		string(card.Kind),
		card.Title,
		card.Summary,
		string(details),
		string(entities),
	).Scan(&memoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSourceUnavailable
	}
	if err != nil {
		return fmt.Errorf("save confirmed memory: %w", err)
	}
	return nil
}

// Find returns the newest active cards matching an exact lookup.
func (s *Store) Find(
	ctx context.Context,
	scope tool.Scope,
	lookup Lookup,
) ([]Card, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.UserID == "" {
		return nil, ErrScopeRequired
	}

	lookup = lookup.Normalize()
	if lookup.Empty() {
		return nil, nil
	}

	topics := make([]string, 0, len(lookup.Topics))
	for _, topic := range lookup.Topics {
		topics = append(topics, string(topic))
	}
	kinds := make([]string, 0, len(lookup.Kinds))
	for _, kind := range lookup.Kinds {
		kinds = append(kinds, string(kind))
	}

	rows, err := s.pool.Query(
		ctx,
		`select jsonb_build_object(
		     'topics', memory.topics,
		     'kind', memory.kind,
		     'title', memory.title,
		     'summary', memory.summary,
		     'details', memory.details,
		     'entities', memory.entities
		 )
		 from public.memories as memory
		 where memory.user_id = $1::uuid
		   and memory.status = 'active'
		   and (cardinality($2::text[]) = 0 or memory.topics && $2::text[])
		   and (cardinality($3::text[]) = 0 or memory.kind = any($3::text[]))
		   and case when cardinality($4::text[]) > 0 then
		       exists (
		           select 1
		           from jsonb_array_elements(memory.entities) as entity(value)
		           where lower(entity.value->>'name') = any($4::text[])
		       )
		   else
		       exists (
		           select 1
		           from unnest($5::text[]) as term(value)
		           where strpos(lower(memory.title || ' ' || memory.summary), term.value) > 0
		       )
		   end
		 order by memory.updated_at desc
		 limit $6`,
		scope.UserID,
		topics,
		kinds,
		lookup.Entities,
		lookup.Terms,
		searchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("find memories: %w", err)
	}
	defer rows.Close()

	cards := make([]Card, 0, searchLimit)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		var card Card
		if err := json.Unmarshal(encoded, &card); err != nil {
			return nil, fmt.Errorf("decode memory: %w", err)
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}

	return cards, nil
}
