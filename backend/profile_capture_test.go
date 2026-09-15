package main

import (
	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"testing"
)

func TestProfileEditsDoNotCreateNewMemories(t *testing.T) {
	for _, action := range []assistant.Action{assistant.ActionProfileInclude, assistant.ActionProfileExclude} {
		if shouldCaptureMemory(action) {
			t.Fatalf("profile action %s queued memory extraction", action)
		}
	}
}
