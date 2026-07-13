package location

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const parametersSchema = `{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`

var (
	ErrLocationReaderRequired = errors.New("location reader is required")
	ErrLocationUnavailable    = errors.New("current location is unavailable")
)

// Tool returns a user's latest known location.
type Tool struct {
	locations Reader
}

func New(locations Reader) (*Tool, error) {
	if locations == nil {
		return nil, ErrLocationReaderRequired
	}

	return &Tool{locations: locations}, nil
}

func (t *Tool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "get_current_location",
		Description: "Get the user's latest known location reported by their device.",
		Parameters:  json.RawMessage(parametersSchema),
		ReadOnly:    true,
	}
}

func (t *Tool) Execute(
	ctx context.Context,
	scope tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	var parsedArguments map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &parsedArguments); err != nil {
		return tool.Result{}, fmt.Errorf("decode arguments: %w", err)
	}
	if parsedArguments == nil || len(parsedArguments) != 0 {
		return tool.Result{}, errors.New("get_current_location does not accept arguments")
	}

	position, found, err := t.locations.Current(ctx, scope.UserID)
	if err != nil {
		return tool.Result{}, fmt.Errorf("get current location: %w", err)
	}
	if !found {
		return tool.Result{}, ErrLocationUnavailable
	}

	content, err := json.Marshal(position)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode current location: %w", err)
	}

	return tool.Result{Content: string(content)}, nil
}
