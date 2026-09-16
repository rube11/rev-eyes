package memory

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	searchLimit       = 8
	maxCandidateBatch = 12
)

var (
	ErrScopeRequired          = errors.New("memory scope is required")
	ErrSourceRequired         = errors.New("source utterance ID is required")
	ErrSourceUnavailable      = errors.New("source utterance is unavailable")
	ErrCandidateBatchTooLarge = errors.New("memory candidate batch is too large")
	ErrDuplicateMemoryKey     = errors.New("memory candidate keys must be unique within a batch")
	ErrMemoryAmbiguous        = errors.New("more than one memory matches the request")
)

// Store persists atomic memories and their transcript sources.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("database pool is required")
	}
	return &Store{pool: pool}
}
