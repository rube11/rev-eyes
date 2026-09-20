package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type ToolState struct {
	Context ResponseContext   `json:"context"`
	Results []ToolObservation `json:"tool_results,omitempty"`
}

type ToolObservation struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Content   string          `json:"content,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type PreparedToolArguments struct {
	Arguments json.RawMessage `json:"arguments"`
}

type ToolClassifier interface {
	Select(context.Context, ToolState, []tool.Spec) ([]string, error)
}

// ToolArgumentBuilder can only fill the supplied specifications, never select tools.
type ToolArgumentBuilder interface {
	Build(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error)
}

type ToolRunResult struct {
	Results         []ToolObservation
	ProposalCreated bool
	ProposalKinds   []ProposalKind
}

type ToolRunner interface {
	Run(context.Context, tool.Scope, ResponseContext) (ToolRunResult, error)
}

// ToolWorkflow owns selection and execution before the final model is invoked.
// Read-only evidence is gathered before proposals are reconsidered. Code, not
// model output, enforces both the turn budget and per-tool attempt budgets.
const (
	MaxWebSearchCalls      = 1
	MaxToolSelectionRounds = 8
	locationToolName       = "get_current_location"
	searchToolName         = "search_web"
	reminderToolName       = "propose_task"
	watchToolName          = "propose_watch"
)

type ToolWorkflow struct {
	registry   *tool.Registry
	classifier ToolClassifier
	arguments  ToolArgumentBuilder
}

func NewToolWorkflow(registry *tool.Registry, classifier ToolClassifier, arguments ToolArgumentBuilder) (*ToolWorkflow, error) {
	if registry == nil || classifier == nil || arguments == nil {
		return nil, errors.New("tool registry, classifier, and argument builder are required")
	}
	return &ToolWorkflow{registry: registry, classifier: classifier, arguments: arguments}, nil
}

func (w *ToolWorkflow) Run(ctx context.Context, scope tool.Scope, turn ResponseContext) (ToolRunResult, error) {
	var result ToolRunResult
	if scope.MemoryReview || turn.MemoryReview {
		return result, nil
	}
	state := ToolState{Context: turn}
	specs := w.registry.Specs()
	if len(specs) == 0 {
		return result, nil
	}
	attempts := map[string]int{}
	for round := 0; round < MaxToolSelectionRounds; round++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		available := make([]tool.Spec, 0, len(specs))
		for _, spec := range specs {
			limit := 1
			if spec.Name == searchToolName {
				limit = MaxWebSearchCalls
			}
			if attempts[spec.Name] < limit {
				available = append(available, spec)
			}
		}
		if len(available) == 0 {
			return result, nil
		}
		state.Results = result.Results
		selected, err := w.selectSpecs(ctx, state, available)
		if err != nil {
			return result, err
		}
		if len(selected) == 0 {
			return result, nil
		}
		// Location is an explicit prerequisite for selected local searches.
		remaining := make([]tool.Spec, 0, len(selected))
		for _, spec := range selected {
			if spec.Name == locationToolName {
				attempts[spec.Name]++
				result.Results = append(result.Results, w.execute(ctx, scope, spec.Name, json.RawMessage(`{}`)))
			} else {
				remaining = append(remaining, spec)
			}
		}
		if len(result.Results) != len(state.Results) {
			// Jev, not the argument builder, decides readiness after location
			// succeeds or fails, just as it does after search evidence arrives.
			continue
		}
		// Never prepare or execute a proposal against pre-search evidence.
		// Re-select it next round with actual results, including cancellation.
		reads := make([]tool.Spec, 0, len(remaining))
		for _, spec := range remaining {
			if spec.ReadOnly {
				reads = append(reads, spec)
			}
		}
		if len(reads) > 0 {
			remaining = reads
		}
		state.Results = result.Results
		before := len(result.Results)
		if err := w.runBatch(ctx, scope, state, remaining, &result); err != nil {
			return result, err
		}
		for _, observation := range result.Results[before:] {
			attempts[observation.Name]++
		}
	}
	result.Results = append(result.Results, ToolObservation{Name: "workflow", Error: "Tool selection round limit reached. No further tools were attempted."})
	return result, nil
}

func (w *ToolWorkflow) selectSpecs(ctx context.Context, state ToolState, available []tool.Spec) ([]tool.Spec, error) {
	names, err := w.classifier.Select(ctx, state, available)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]tool.Spec, len(available))
	for _, spec := range available {
		allowed[spec.Name] = spec
	}
	selected := make([]tool.Spec, 0, len(names))
	for _, name := range names {
		spec, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("tool classifier selected unavailable or duplicate tool %q", name)
		}
		delete(allowed, name)
		selected = append(selected, spec)
	}
	return selected, nil
}

func (w *ToolWorkflow) runBatch(ctx context.Context, scope tool.Scope, state ToolState, specs []tool.Spec, result *ToolRunResult) error {
	if len(specs) == 0 {
		return nil
	}
	prepared, err := w.arguments.Build(ctx, state, specs)
	if err != nil {
		return fmt.Errorf("prepare tool arguments: %w", err)
	}
	if len(prepared) != len(specs) {
		return errors.New("argument builder did not return exactly the selected tools")
	}
	// Validate the whole batch before any effects are possible.
	for _, spec := range specs {
		args, ok := prepared[spec.Name]
		if !ok {
			return fmt.Errorf("argument builder omitted %q", spec.Name)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(args.Arguments, &object); err != nil || object == nil {
			return fmt.Errorf("arguments for %q must be an object", spec.Name)
		}
	}
	observations := make([]ToolObservation, len(specs))
	run := func(index int) {
		spec := specs[index]
		args := prepared[spec.Name]
		observations[index] = w.execute(ctx, scope, spec.Name, args.Arguments)
	}
	var group sync.WaitGroup
	for index, spec := range specs {
		if spec.ReadOnly {
			group.Add(1)
			go func() { defer group.Done(); run(index) }()
		}
	}
	group.Wait()
	for index, spec := range specs {
		if !spec.ReadOnly && ctx.Err() == nil {
			run(index)
		}
	}
	for _, observation := range observations {
		if observation.Name == "" {
			continue
		}
		result.Results = append(result.Results, observation)
		if observation.Error == "" && (observation.Name == reminderToolName || observation.Name == watchToolName) {
			var status struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(observation.Content), &status) == nil && status.Status == "proposed" {
				result.ProposalCreated = true
				kind := ProposalTask
				if observation.Name == watchToolName {
					kind = ProposalWatch
				}
				result.ProposalKinds = append(result.ProposalKinds, kind)
			}
		}
	}
	return ctx.Err()
}

func (w *ToolWorkflow) execute(ctx context.Context, scope tool.Scope, name string, args json.RawMessage) ToolObservation {
	observation := ToolObservation{Name: name, Arguments: args}
	if err := ctx.Err(); err != nil {
		observation.Error = err.Error()
		return observation
	}
	started := time.Now()
	result, err := w.registry.Execute(ctx, scope, name, args)
	slog.InfoContext(ctx, "tool executed", "name", name, "success", err == nil, "duration_ms", time.Since(started).Milliseconds())
	if err != nil {
		observation.Error = err.Error()
	} else {
		observation.Content = result.Content
	}
	return observation
}
