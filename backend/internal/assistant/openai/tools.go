package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	proposeTaskToolName  = "propose_task"
	proposeWatchToolName = "propose_watch"
)

func (a *Agent) executeCalls(
	ctx context.Context,
	scope tool.Scope,
	calls []toolCall,
) ([]json.RawMessage, error) {
	if !a.canRunInParallel(calls) {
		outputs := make([]json.RawMessage, 0, len(calls))
		for _, call := range calls {
			output, err := a.executeCall(ctx, scope, call)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, output)
		}
		return outputs, nil
	}

	outputs := make([]json.RawMessage, len(calls))
	callErrors := make([]error, len(calls))
	var group sync.WaitGroup
	group.Add(len(calls))

	for index, call := range calls {
		go func() {
			defer group.Done()
			outputs[index], callErrors[index] = a.executeCall(ctx, scope, call)
		}()
	}
	group.Wait()

	for _, err := range callErrors {
		if err != nil {
			return nil, err
		}
	}
	return outputs, nil
}

func proposalCreatedBy(calls []toolCall, outputs []json.RawMessage) bool {
	if len(calls) != len(outputs) {
		return false
	}
	for index, call := range calls {
		if call.Name != proposeTaskToolName && call.Name != proposeWatchToolName {
			continue
		}
		var output toolOutput
		if err := json.Unmarshal(outputs[index], &output); err != nil {
			continue
		}
		var result struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(output.Output), &result); err == nil &&
			result.Status == "proposed" {
			return true
		}
	}
	return false
}

func (a *Agent) canRunInParallel(calls []toolCall) bool {
	if len(calls) < 2 {
		return false
	}

	for _, call := range calls {
		selected, found := a.registry.Get(call.Name)
		if !found || !selected.Spec().ReadOnly {
			return false
		}
	}
	return true
}

func (a *Agent) executeCall(
	ctx context.Context,
	scope tool.Scope,
	call toolCall,
) (json.RawMessage, error) {
	startedAt := time.Now()
	result, err := a.executor.Execute(ctx, scope, call.Name, call.Arguments)
	slog.InfoContext(ctx, "tool executed",
		"name", call.Name,
		"success", err == nil,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		errorOutput, _ := json.Marshal(map[string]string{"error": err.Error()})
		result.Content = string(errorOutput)
	} else if strings.TrimSpace(result.Content) == "" {
		result.Content = "success"
	}

	output, err := json.Marshal(toolOutput{
		Type:   "function_call_output",
		CallID: call.CallID,
		Output: result.Content,
	})
	if err != nil {
		return nil, fmt.Errorf("encode tool output: %w", err)
	}
	return output, nil
}
