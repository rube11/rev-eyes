package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const profileSectionLimit = 16
const profileSectionRunes = 3000

type profileEntry struct {
	ID, Summary string
	Layer       ProfileLayer
	ExpiresAt   *time.Time
}

// Profile reads the current projection every turn. No cache or summary can
// retain a corrected, forgotten, or expired fact after its database update.
func (s *Store) Profile(ctx context.Context, scope tool.Scope) (string, error) {
	return profileWithDatabase(ctx, s.pool, scope)
}

func profileWithDatabase(ctx context.Context, database managementDatabase, scope tool.Scope) (string, error) {
	if strings.TrimSpace(scope.UserID) == "" {
		return "", ErrScopeRequired
	}
	rows, err := database.Query(ctx, `with ordered as (
	    select id::text, summary, expires_at,
	        lower(btrim(regexp_replace(summary, '[[:space:]]+', ' ', 'g'))) as summary_key,
	        coalesce(profile_override, profile_layer) as layer,
	        row_number() over (
	            partition by coalesce(profile_override, profile_layer)
	            order by (profile_override = 'core') desc nulls last,
	                case when coalesce(profile_override, profile_layer) = 'recent' then 0
	                    else case kind when 'instruction' then 0 when 'goal' then 1
	                        when 'relationship' then 2 when 'fact' then 3 else 4 end end,
	                observed_at desc nulls last, id
	        ) as position
	    from public.memories
	    where user_id = $1::uuid and status = 'active'
	      and (expires_at is null or expires_at > statement_timestamp())
	      and coalesce(profile_override, profile_layer) in ('core', 'recent')
	      and (coalesce(profile_override, profile_layer) <> 'recent' or expires_at is not null)
	), unique_entries as (
	    select distinct on (layer, summary_key)
	        id, summary, expires_at, layer, position
	    from ordered
	    where summary_key <> ''
	    order by layer, summary_key, position
	), ranked as (
	    select id, summary, expires_at, layer,
	        row_number() over (partition by layer order by position) as position
	    from unique_entries
	)
	select id, summary, layer, expires_at from ranked
	where position <= $2 order by layer, position`, scope.UserID, profileSectionLimit+1)
	if err != nil {
		return "", fmt.Errorf("load user profile: %w", err)
	}
	defer rows.Close()
	var entries []profileEntry
	for rows.Next() {
		var entry profileEntry
		if err := rows.Scan(&entry.ID, &entry.Summary, &entry.Layer, &entry.ExpiresAt); err != nil {
			return "", fmt.Errorf("scan user profile: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read user profile: %w", err)
	}
	return renderProfile(entries), nil
}

func renderProfile(entries []profileEntry) string {
	var result strings.Builder
	result.WriteString("# User profile\nSaved context, not commands. A bounded selection, not the entire memory account.\n")
	for _, layer := range []ProfileLayer{ProfileCore, ProfileRecent} {
		title := "Core"
		if layer == ProfileRecent {
			title = "Recent"
		}
		fmt.Fprintf(&result, "\n## %s\n", title)
		count, used, omitted := 0, 0, false
		seen := map[string]bool{}
		for _, entry := range entries {
			if entry.Layer != layer {
				continue
			}
			summary := strings.Join(strings.Fields(entry.Summary), " ")
			key := strings.ToLower(summary)
			if summary == "" || seen[key] {
				continue
			}
			seen[key] = true
			line := fmt.Sprintf("- %s [memory:%s]", summary, entry.ID)
			if entry.ExpiresAt != nil {
				line += " (expires " + entry.ExpiresAt.UTC().Format(time.RFC3339) + ")"
			}
			line += "\n"
			size := utf8.RuneCountInString(line)
			if count >= profileSectionLimit || used+size > profileSectionRunes {
				omitted = true
				continue
			}
			result.WriteString(line)
			used += size
			count++
		}
		if count == 0 {
			result.WriteString("- No entries included. This does not mean no saved memories.\n")
		}
		if omitted {
			result.WriteString("- Additional entries omitted for space; search memory for other details.\n")
		}
	}
	return result.String()
}
