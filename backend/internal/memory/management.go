package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type managementDatabase interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Review returns matched memories, or the most recently updated memories when
// the user asks to inspect their whole memory profile.
func (s *Store) Review(
	ctx context.Context,
	scope tool.Scope,
	lookup Lookup,
) ([]Card, error) {
	lookup = lookup.Normalize()
	if hasLookupFilters(lookup) {
		return s.Find(ctx, scope, lookup)
	}
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.UserID == "" {
		return nil, ErrScopeRequired
	}
	return reviewRecent(ctx, s.pool, scope.UserID)
}

func reviewRecent(
	ctx context.Context,
	database managementDatabase,
	userID string,
) ([]Card, error) {
	rows, err := database.Query(
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
		   and (memory.expires_at is null or memory.expires_at > statement_timestamp())
		 order by memory.updated_at desc
		 limit $2`,
		userID,
		searchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("review memories: %w", err)
	}
	defer rows.Close()

	cards := make([]Card, 0, searchLimit)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan reviewed memory: %w", err)
		}
		var card Card
		if err := json.Unmarshal(encoded, &card); err != nil {
			return nil, fmt.Errorf("decode reviewed memory: %w", err)
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read reviewed memories: %w", err)
	}
	return cards, nil
}

// Forget marks one unambiguous active memory as forgotten. Broad requests are
// never allowed to silently remove multiple cards; the caller must ask the
// user to identify one of the matches.
func (s *Store) Forget(
	ctx context.Context,
	scope tool.Scope,
	lookup Lookup,
) (int, error) {
	return forgetWithDatabase(ctx, s.pool, scope, lookup)
}

func forgetWithDatabase(
	ctx context.Context,
	database managementDatabase,
	scope tool.Scope,
	lookup Lookup,
) (int, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.UserID == "" {
		return 0, ErrScopeRequired
	}
	lookup = lookup.Normalize()
	if !hasLookupFilters(lookup) {
		return 0, nil
	}

	topics := make([]string, len(lookup.Topics))
	for index, topic := range lookup.Topics {
		topics[index] = string(topic)
	}
	kinds := make([]string, len(lookup.Kinds))
	for index, kind := range lookup.Kinds {
		kinds[index] = string(kind)
	}

	var matched int
	var forgotten bool
	err := database.QueryRow(
		ctx,
		`with parameters as (
		     select case when $5::text = '' then null::tsquery
		                 else websearch_to_tsquery('english'::regconfig, $5::text)
		            end as text_query
		 ),
		 scored as (
		     select
		         memory.id,
		         case when parameters.text_query is null then 0::real
		              else ts_rank_cd(memory.search_document, parameters.text_query)
		         end as text_score,
		         parameters.text_query is not null as has_text,
		         cardinality($4::text[]) > 0 as has_entities,
		         cardinality($2::text[]) > 0 and memory.topics && $2::text[] as topic_hit,
		         cardinality($3::text[]) > 0 and memory.kind = any($3::text[]) as kind_hit,
		         cardinality($4::text[]) > 0 and exists (
		             select 1
		             from jsonb_array_elements(memory.entities) as entity(value)
		             where lower(entity.value->>'name') = any($4::text[])
		         ) as entity_hit,
		         memory.updated_at
		     from public.memories as memory
		     cross join parameters
		     where memory.user_id = $1::uuid
		       and memory.status = 'active'
		       and (memory.expires_at is null or memory.expires_at > statement_timestamp())
		 ),
		 matches as (
		     select scored.id
		     from scored
		     where (scored.has_text and scored.text_score > 0)
		        or (not scored.has_text and scored.has_entities and scored.entity_hit)
		        or (
		            not scored.has_text
		            and not scored.has_entities
		            and scored.topic_hit
		            and scored.kind_hit
		        )
		     order by scored.entity_hit desc, scored.text_score desc, scored.updated_at desc
		     limit 2
		 ),
		 choice as (
		     select matches.id
		     from matches
		     where (select count(*) from matches) = 1
		 ),
		 updated as (
		     update public.memories as memory
		     set status = 'forgotten',
		         inactive_at = statement_timestamp(),
		         updated_at = statement_timestamp()
		     from choice
		     where memory.id = choice.id
		       and memory.user_id = $1::uuid
		       and memory.status = 'active'
		     returning memory.id
		 )
		 select
		     (select count(*) from matches),
		     exists(select 1 from updated)`,
		scope.UserID,
		topics,
		kinds,
		lookup.Entities,
		webSearchQuery(lookup.Query, lookup.Terms),
	).Scan(&matched, &forgotten)
	if err != nil {
		return 0, fmt.Errorf("forget memory: %w", err)
	}
	if matched > 1 {
		return 0, ErrMemoryAmbiguous
	}
	if matched == 0 {
		return 0, nil
	}
	if !forgotten {
		return 0, fmt.Errorf("forget memory: matched memory changed before update")
	}
	return 1, nil
}

func hasLookupFilters(lookup Lookup) bool {
	return !lookup.Empty() || len(lookup.Topics) > 0 || len(lookup.Kinds) > 0
}
