package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Execute validates a tool call, resolves it through the registry, and runs it.
func (r *Registry) Execute(
	ctx context.Context,
	scope Scope,
	name string,
	arguments json.RawMessage,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Result{}, ErrEmptyToolName
	}

	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(arguments) {
		return Result{}, fmt.Errorf("tool %q received invalid JSON arguments", name)
	}

	selected, found := r.Get(name)
	if !found {
		return Result{}, fmt.Errorf("tool %q is not registered", name)
	}

	result, err := selected.Execute(ctx, scope, arguments)
	if err != nil {
		return Result{}, fmt.Errorf("execute tool %q: %w", name, err)
	}

	return result, nil
}
