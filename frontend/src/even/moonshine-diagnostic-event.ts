import type { CandidateCategory } from "./candidate-gate.js"

export type MoonshineDiagnosticEvent =
  | {
      event: "transcript"
      kind: "partial" | "committed"
      text: string
    }
  | {
      event: "lifecycle"
      name: string
    }
  | {
      event: "candidate_trigger"
      category: CandidateCategory
      confidence: number
      sample_offset: number
    }
  | {
      event: "candidate_finalized"
      reason: string
      category: CandidateCategory
      byte_length: number
      start_sample_offset: number
      end_sample_offset: number
      submitted: boolean
    }
