package candidate

import "testing"

func TestMatchWakePhraseRecognizesApprovedFamilies(t *testing.T) {
	tests := []struct {
		transcript string
		reason     WakeReason
	}{
		{"Hey, Glasses, what time is it?", WakeAssistantRequest},
		{"Glasses, what time is it?", WakeAssistantRequest},
		{"My glasses are on the table.", WakeAssistantRequest},
		{"Remember that Maya is my manager.", WakeAssistantRequest},
		{"Please remember this preference.", WakeAssistantRequest},
		{"Show that again.", WakeAssistantRequest},
		{"Repeat that, please.", WakeAssistantRequest},
		{"Remind me to call Mom tomorrow.", WakeReminder},
		{"Don't let me forget the groceries.", WakeReminder},
		{"Don’t let me forget my keys.", WakeReminder},
		{"Dont let me forget my badge.", WakeReminder},
		{"I need to go to the gym after class.", WakeCommitment},
		{"Need to buy groceries tomorrow.", WakeCommitment},
		{"I need groceries tomorrow.", WakeCommitment},
		{"I should call the dentist tomorrow.", WakeCommitment},
		{"I plan to visit Maya next week.", WakeIntention},
		{"I want to exercise after class.", WakeIntention},
		{"I prefer window seats.", WakePreference},
	}

	for _, test := range tests {
		t.Run(test.transcript, func(t *testing.T) {
			reason, matched := MatchWakePhrase(test.transcript)
			if !matched || reason != test.reason {
				t.Fatalf("MatchWakePhrase() = (%q, %t), want (%q, true)", reason, matched, test.reason)
			}
		})
	}
}

func TestMatchWakePhraseRejectsUnapprovedSpeech(t *testing.T) {
	tests := []string{
		"",
		"The meeting starts at three.",
		"Could you find the nearest coffee shop?",
		"What time is the meeting?",
		"Please help me with this.",
		"I really love window seats.",
		"I will call the dentist tomorrow.",
		"Do you remember that movie?",
		"The needle is upstairs.",
		"That was needless work.",
	}

	for _, transcript := range tests {
		t.Run(transcript, func(t *testing.T) {
			if reason, matched := MatchWakePhrase(transcript); matched {
				t.Fatalf("MatchWakePhrase() = (%q, true), want no match", reason)
			}
		})
	}
}
