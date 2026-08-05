export type CandidateCategory =
  | "assistant_request"
  | "commitment"
  | "intention"
  | "manual"
  | "preference"
  | "reminder"

export type CandidateTrigger = {
  category: CandidateCategory
  confidence: number
}

type GateRule = CandidateTrigger & {
  patterns: readonly RegExp[]
}

const DUPLICATE_COOLDOWN_MS = 4_000
const APPROXIMATE_WAKE_CONFIDENCE = 0.78
const REQUEST_SHAPE_CONFIDENCE = 0.68
const ROUGH_PREFIX_TOKEN_LIMIT = 3
const ROUGH_WAKE_START_LIMIT = 2

const questionWords = new Set([
  "how",
  "what",
  "when",
  "where",
  "which",
  "who",
  "why",
])

const questionAuxiliaries = new Set([
  "are",
  "can",
  "could",
  "did",
  "do",
  "does",
  "is",
  "should",
  "was",
  "were",
  "will",
  "would",
])

const questionSubjects = new Set([
  "i",
  "it",
  "that",
  "there",
  "this",
  "we",
  "you",
])

const requestVerbs = new Set(["find", "give", "help", "show", "take", "tell"])
const nonWakeLeads = new Set([
  "her",
  "his",
  "my",
  "our",
  "the",
  "their",
  "these",
  "those",
  "your",
])

const rules: readonly GateRule[] = [
  {
    category: "reminder",
    confidence: 0.98,
    patterns: [
      /\bremind\s+me\b/u,
      /\bdon['’]?t\s+let\s+me\s+forget\b/u,
    ],
  },
  {
    category: "assistant_request",
    confidence: 0.98,
    patterns: [
      /\bglasses\b/u,
      /^(?:please\s+)?remember\b/u,
      /\b(?:show\s+(?:that|it)\s+again|repeat\s+that)\b/u,
    ],
  },
  {
    category: "commitment",
    confidence: 0.9,
    patterns: [
      /\bneed(?:\s+to)?\b/u,
      /\bi\s+should\b/u,
    ],
  },
  {
    category: "intention",
    confidence: 0.88,
    patterns: [
      /\bi\s+plan\s+to\b/u,
      /\bi\s+want\s+to\b/u,
    ],
  },
  {
    category: "preference",
    confidence: 0.86,
    patterns: [/\bi\s+prefer\b/u],
  },
]

function normalizeTranscript(text: string): string {
  return text
    .normalize("NFKC")
    .toLocaleLowerCase("en-US")
    .replace(/[^\p{L}\p{N}'’]+/gu, " ")
    .trim()
    .replace(/\s+/gu, " ")
}

function editDistanceAtMost(left: string, right: string, limit: number): boolean {
  if (Math.abs(left.length - right.length) > limit) {
    return false
  }

  let previous = Array.from({ length: right.length + 1 }, (_, index) => index)
  for (let leftIndex = 1; leftIndex <= left.length; leftIndex += 1) {
    const current = [leftIndex]
    for (let rightIndex = 1; rightIndex <= right.length; rightIndex += 1) {
      current[rightIndex] = Math.min(
        current[rightIndex - 1] + 1,
        previous[rightIndex] + 1,
        previous[rightIndex - 1] +
          (left[leftIndex - 1] === right[rightIndex - 1] ? 0 : 1),
      )
    }
    previous = current
  }
  return previous[right.length] <= limit
}

function resemblesGlassesNearStart(tokens: readonly string[]): boolean {
  const prefixLength = Math.min(tokens.length, ROUGH_PREFIX_TOKEN_LIMIT)
  const startLimit = Math.min(prefixLength, ROUGH_WAKE_START_LIMIT)
  for (let start = 0; start < startLimit; start += 1) {
    for (let width = 1; width <= 2 && start + width <= prefixLength; width += 1) {
      if (
        (start > 0 && nonWakeLeads.has(tokens[start - 1])) ||
        (width > 1 && nonWakeLeads.has(tokens[start]))
      ) {
        continue
      }
      const compact = tokens.slice(start, start + width).join("")
      if (compact.length < 4) {
        continue
      }
      const distanceLimit = width === 1 ? 2 : 3
      if (editDistanceAtMost(compact, "glasses", distanceLimit)) {
        return true
      }
    }
  }
  return false
}

function resemblesQuestionOrRequest(
  text: string,
  tokens: readonly string[],
): boolean {
  if (text.trimEnd().endsWith("?")) {
    return true
  }

  const prefixLength = Math.min(tokens.length, ROUGH_PREFIX_TOKEN_LIMIT + 1)
  for (let index = 0; index < prefixLength; index += 1) {
    const token = tokens[index]
    const next = tokens[index + 1]
    if (token === "please") {
      return true
    }
    if (questionWords.has(token)) {
      const nearbyQuestionAuxiliary = tokens
        .slice(index + 1, index + 4)
        .some((candidate) => questionAuxiliaries.has(candidate))
      if (index === 0 || nearbyQuestionAuxiliary) {
        return true
      }
    }
    if (
      next !== undefined &&
      ((questionAuxiliaries.has(token) && questionSubjects.has(next)) ||
        (requestVerbs.has(token) && next === "me") ||
        (token === "look" && next === "up"))
    ) {
      return true
    }
  }
  return false
}

function roughAssistantRequest(
  text: string,
  normalized: string,
): CandidateTrigger | undefined {
  // This fallback intentionally favors candidate recall. The backend still
  // requires the accurate Deepgram transcript to match an approved wake phrase
  // before it invokes the assistant or executes an action.
  const tokens = normalized.split(" ")
  if (resemblesGlassesNearStart(tokens)) {
    return {
      category: "assistant_request",
      confidence: APPROXIMATE_WAKE_CONFIDENCE,
    }
  }
  if (resemblesQuestionOrRequest(text, tokens)) {
    return {
      category: "assistant_request",
      confidence: REQUEST_SHAPE_CONFIDENCE,
    }
  }
  return undefined
}

export class CandidateGate {
  private lastTranscript = ""
  private lastTriggeredAt = Number.NEGATIVE_INFINITY

  evaluate(text: string, now = Date.now()): CandidateTrigger | undefined {
    const normalized = normalizeTranscript(text)
    if (normalized.length === 0) {
      return undefined
    }
    if (
      normalized === this.lastTranscript &&
      now - this.lastTriggeredAt < DUPLICATE_COOLDOWN_MS
    ) {
      return undefined
    }

    for (const rule of rules) {
      if (!rule.patterns.some((pattern) => pattern.test(normalized))) {
        continue
      }
      this.lastTranscript = normalized
      this.lastTriggeredAt = now
      return {
        category: rule.category,
        confidence: rule.confidence,
      }
    }
    const roughRequest = roughAssistantRequest(text, normalized)
    if (roughRequest) {
      this.lastTranscript = normalized
      this.lastTriggeredAt = now
      return roughRequest
    }
    return undefined
  }

  reset(): void {
    this.lastTranscript = ""
    this.lastTriggeredAt = Number.NEGATIVE_INFINITY
  }
}
