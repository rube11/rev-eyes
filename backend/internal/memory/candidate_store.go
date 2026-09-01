package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type encodedCandidate struct {
	Candidate
	Topics   []string
	Details  string
	Entities string
}

// RememberCandidates stores one extracted batch atomically. A canonical key
// identifies the active row, while observed_at prevents delayed background
// work from replacing a newer user statement.
func (s *Store) RememberCandidates(
	ctx context.Context,
	scope tool.Scope,
	sourceUtteranceID string,
	candidates []Candidate,
) (int, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return 0, ErrScopeRequired
	}
	sourceUtteranceID = strings.TrimSpace(sourceUtteranceID)
	if sourceUtteranceID == "" {
		return 0, ErrSourceRequired
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	if len(candidates) > maxCandidateBatch {
		return 0, ErrCandidateBatchTooLarge
	}

	encoded := make([]encodedCandidate, 0, len(candidates))
	seenKeys := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = candidate.Normalize()
		if err := candidate.Validate(); err != nil {
			return 0, err
		}
		if candidate.Retention == RetentionTemporary && candidate.ExpiresAt == nil {
			return 0, fmt.Errorf(
				"%w: temporary memory requires an expiration",
				ErrCandidateInvalid,
			)
		}
		if _, exists := seenKeys[candidate.MemoryKey]; exists {
			return 0, ErrDuplicateMemoryKey
		}
		seenKeys[candidate.MemoryKey] = struct{}{}
		prepared, err := encodeCandidate(candidate)
		if err != nil {
			return 0, err
		}
		encoded = append(encoded, prepared)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin memory transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sourceStartedAt time.Time
	if err := tx.QueryRow(
		ctx,
		`select started_at
		 from public.transcript_utterances
		 where id = $3::uuid
		   and user_id = $1::uuid
		   and session_id = $2::uuid
		   and speaker = 'user'`,
		scope.UserID,
		scope.SessionID,
		sourceUtteranceID,
	).Scan(&sourceStartedAt); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSourceUnavailable
	} else if err != nil {
		return 0, fmt.Errorf("resolve memory source: %w", err)
	}

	remembered := 0
	for _, candidate := range encoded {
		if candidate.ExpiresAt != nil && !candidate.ExpiresAt.After(sourceStartedAt) {
			return 0, fmt.Errorf(
				"%w: expiration must follow the source utterance",
				ErrCandidateInvalid,
			)
		}
		accepted, err := upsertCandidate(
			ctx,
			tx,
			scope.UserID,
			sourceUtteranceID,
			sourceStartedAt,
			candidate,
		)
		if err != nil {
			return 0, err
		}
		if accepted {
			remembered++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit memory candidates: %w", err)
	}
	return remembered, nil
}

func encodeCandidate(candidate Candidate) (encodedCandidate, error) {
	details, err := json.Marshal(candidate.Card.Details)
	if err != nil {
		return encodedCandidate{}, fmt.Errorf("encode memory details: %w", err)
	}
	entities, err := json.Marshal(candidate.Card.Entities)
	if err != nil {
		return encodedCandidate{}, fmt.Errorf("encode memory entities: %w", err)
	}
	topics := make([]string, len(candidate.Card.Topics))
	for index, topic := range candidate.Card.Topics {
		topics[index] = string(topic)
	}
	return encodedCandidate{
		Candidate: candidate,
		Topics:    topics,
		Details:   string(details),
		Entities:  string(entities),
	}, nil
}

func upsertCandidate(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	sourceUtteranceID string,
	observedAt time.Time,
	candidate encodedCandidate,
) (bool, error) {
	var memoryID string
	err := tx.QueryRow(
		ctx,
		`insert into public.memories as existing (
		     user_id,
		     memory_key,
		     topics,
		     kind,
		     title,
		     summary,
		     details,
		     entities,
		     observed_at,
		     observed_source_id,
		     expires_at
		 ) values (
		     $1::uuid,
		     $2,
		     $3,
		     $4,
		     $5,
		     $6,
		     $7::jsonb,
		     $8::jsonb,
		     $9::timestamptz,
		     $10::uuid,
		     $11::timestamptz
		 )
		 on conflict (user_id, memory_key)
		     where status = 'active' and memory_key is not null
		 do update set
		     topics = excluded.topics,
		     kind = excluded.kind,
		     title = excluded.title,
		     summary = excluded.summary,
		     details = excluded.details,
		     entities = excluded.entities,
		     observed_at = excluded.observed_at,
		     observed_source_id = excluded.observed_source_id,
		     expires_at = excluded.expires_at,
		     updated_at = statement_timestamp()
		 where (
		     existing.observed_at,
		     coalesce(
		         existing.observed_source_id,
		         '00000000-0000-0000-0000-000000000000'::uuid
		     )
		 ) < (excluded.observed_at, excluded.observed_source_id)
		 returning existing.id::text`,
		userID,
		candidate.MemoryKey,
		candidate.Topics,
		string(candidate.Card.Kind),
		candidate.Card.Title,
		candidate.Card.Summary,
		candidate.Details,
		candidate.Entities,
		observedAt,
		sourceUtteranceID,
		candidate.ExpiresAt,
	).Scan(&memoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("upsert memory candidate: %w", err)
	}

	command, err := tx.Exec(
		ctx,
		`insert into public.memory_sources (user_id, memory_id, utterance_id)
		 values ($1::uuid, $2::uuid, $3::uuid)
		 on conflict (memory_id, utterance_id) do nothing`,
		userID,
		memoryID,
		sourceUtteranceID,
	)
	if err != nil {
		return false, fmt.Errorf("link memory candidate source: %w", err)
	}
	return command.RowsAffected() == 1, nil
}

// Find retrieves active, unexpired cards using native full-text search and
// the structured hints already produced by the router. Text and exact entity
// hits qualify directly; structured hints qualify only as a topic-kind pair.
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
	if lookup.Empty() && len(lookup.Topics) == 0 && len(lookup.Kinds) == 0 {
		return nil, nil
	}

	topics := make([]string, len(lookup.Topics))
	for index, topic := range lookup.Topics {
		topics[index] = string(topic)
	}
	kinds := make([]string, len(lookup.Kinds))
	for index, kind := range lookup.Kinds {
		kinds[index] = string(kind)
	}

	rows, err := s.pool.Query(
		ctx,
		`with parameters as (
		     select case when $6::text = '' then null::tsquery
		                 else websearch_to_tsquery('english'::regconfig, $6::text)
		            end as text_query
		 ),
		 scored as (
		     select
		         memory.*,
		         case when parameters.text_query is null then 0::real
		              else ts_rank_cd(memory.search_document, parameters.text_query)
		         end as text_score,
		         cardinality($2::text[]) > 0 and memory.topics && $2::text[]
		             as topic_hit,
		         cardinality($3::text[]) > 0 and memory.kind = any($3::text[])
		             as kind_hit,
		         cardinality($4::text[]) > 0 and exists (
		             select 1
		             from jsonb_array_elements(memory.entities) as entity(value)
		             where lower(entity.value->>'name') = any($4::text[])
		         ) as entity_hit
		     from public.memories as memory
		     cross join parameters
		     where memory.user_id = $1::uuid
		       and memory.status = 'active'
		       and (memory.expires_at is null or memory.expires_at > statement_timestamp())
		 )
		 select jsonb_build_object(
		     'topics', scored.topics,
		     'kind', scored.kind,
		     'title', scored.title,
		     'summary', scored.summary,
		     'details', scored.details,
		     'entities', scored.entities
		 )
		 from scored
		 where scored.text_score > 0
		    or scored.entity_hit
		    or (scored.topic_hit and scored.kind_hit)
		 order by
		     scored.entity_hit desc,
		     scored.text_score desc,
		     (scored.topic_hit and scored.kind_hit) desc,
		     scored.updated_at desc
		 limit $5`,
		scope.UserID,
		topics,
		kinds,
		lookup.Entities,
		searchLimit,
		webSearchQuery(lookup.Query, lookup.Terms),
	)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
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

func webSearchQuery(query string, terms []string) string {
	parts := make([]string, 0, len(terms)+1)
	query = strings.TrimSpace(strings.ReplaceAll(query, `"`, " "))
	if query != "" {
		parts = append(parts, query)
	}
	for _, term := range terms {
		term = strings.TrimSpace(strings.ReplaceAll(term, `"`, " "))
		if term != "" {
			parts = append(parts, `"`+term+`"`)
		}
	}
	return strings.Join(parts, " OR ")
}
