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
	ErrDatabaseRequired       = errors.New("memory database is required")
	ErrScopeRequired          = errors.New("memory scope is required")
	ErrSourceRequired         = errors.New("source utterance ID is required")
	ErrSourceUnavailable      = errors.New("source utterance is unavailable")
	ErrCandidateBatchTooLarge = errors.New("memory candidate batch is too large")
	ErrDuplicateMemoryKey     = errors.New("memory candidate keys must be unique within a batch")
)

// Store persists atomic memories and their transcript sources.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}
