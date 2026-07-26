import {
  RebuildPageContainer,
  TextContainerProperty,
} from "@evenrealities/even_hub_sdk"

const DISPLAY_WIDTH = 576
const DISPLAY_HEIGHT = 288
const COMPACT_CARD_WIDTH = 220
const COMPACT_CARD_HEIGHT = 36
const TRANSCRIPT_CARD_WIDTH = 360
const TRANSCRIPT_CARD_HEIGHT = 56
const DISPLAY_MARGIN = 24

export type GlassesMessage =
  | { kind: "answer"; body: string }
  | { kind: "results"; intro: string; items: string[] }
  | { kind: "reminder" | "update"; body: string }

const listItemPattern = /^\s*(?:[-*•]|\d{1,2}[.)])\s+(.+)$/u

function truncate(value: string, limit: number): string {
  const characters = Array.from(value.trim())
  return characters.length <= limit
    ? characters.join("")
    : `${characters.slice(0, limit - 1).join("")}…`
}

function tail(value: string, limit: number): string {
  const characters = Array.from(value.trim())
  return characters.length <= limit
    ? characters.join("")
    : `…${characters.slice(-(limit - 1)).join("")}`
}

function cleanLine(value: string): string {
  return value
    .replace(/\[(.+?)\]\(https?:\/\/[^)]+\)/giu, "$1")
    .replace(/\*\*|__/gu, "")
    .replace(/`([^`]+)`/gu, "$1")
    .replace(/^#{1,6}\s+/u, "")
    .replace(/\s+/gu, " ")
    .trim()
}

function cleanText(value: string): string {
  const listMarkers = Array.from(value.matchAll(/(?:^|\s)\d{1,2}[.)]\s+/gu))
  const expanded = listMarkers.length > 1
    ? value.replace(/\s+(?=\d{1,2}[.)]\s+)/gu, "\n")
    : value

  return expanded
    .replace(/\r\n?/gu, "\n")
    .split("\n")
    .map(cleanLine)
    .join("\n")
    .replace(/\n{3,}/gu, "\n\n")
    .trim()
}

export function presentGlassesMessage(text: string): GlassesMessage {
  const cleaned = cleanText(text)
  if (/^reminder\s*:/iu.test(cleaned)) {
    return {
      kind: "reminder",
      body: truncate(cleaned.replace(/^reminder\s*:\s*/iu, ""), 250),
    }
  }
  if (/^possible update on\s+/iu.test(cleaned)) {
    return { kind: "update", body: truncate(cleaned, 250) }
  }

  const lines = cleaned.split("\n").filter(Boolean)
  const firstItem = lines.findIndex((line) => listItemPattern.test(line))
  const items = lines
    .map((line) => line.match(listItemPattern)?.[1])
    .filter((item): item is string => Boolean(item))

  if (items.length >= 2) {
    return {
      kind: "results",
      intro: truncate(lines.slice(0, firstItem).join(" ") || "Top matches", 74),
      items: items.slice(0, 3),
    }
  }
  return { kind: "answer", body: truncate(cleaned, 340) }
}

function text(data: Partial<TextContainerProperty>): TextContainerProperty {
  return new TextContainerProperty(data)
}

function page(containers: TextContainerProperty[]): RebuildPageContainer {
  return new RebuildPageContainer({
    containerTotalNum: containers.length,
    textObject: containers,
  })
}

export function buildSleepPage(): RebuildPageContainer {
  return page([
    text({
      xPosition: 0,
      yPosition: 0,
      width: DISPLAY_WIDTH,
      height: DISPLAY_HEIGHT,
      containerID: 1,
      containerName: "sleep-wake",
      // Keep an event target mounted without drawing anything in the HUD.
      content: " ",
      isEventCapture: 1,
    }),
  ])
}

export function buildCompactPage(content: string): RebuildPageContainer {
  return page([
    text({
      xPosition: DISPLAY_WIDTH - COMPACT_CARD_WIDTH - DISPLAY_MARGIN,
      yPosition: DISPLAY_HEIGHT - COMPACT_CARD_HEIGHT - DISPLAY_MARGIN,
      width: COMPACT_CARD_WIDTH,
      height: COMPACT_CARD_HEIGHT,
      borderWidth: 1,
      borderRadius: 10,
      paddingLength: 8,
      containerID: 1,
      containerName: "compact-control",
      content: truncate(cleanText(content).replace(/\n/gu, " "), 28).toUpperCase(),
      isEventCapture: 1,
    }),
  ])
}

export function buildTranscriptPage(
  content: string,
  thinkingFrame?: number,
): RebuildPageContainer {
  const transcriptContent = buildTranscriptContent(content, thinkingFrame)
  const thinking = thinkingFrame !== undefined
  const transcript = tail(
    cleanText(content).replace(/\n/gu, " "),
    thinking ? 66 : 82,
  )
  const width = transcript ? TRANSCRIPT_CARD_WIDTH : COMPACT_CARD_WIDTH
  const height = transcript ? TRANSCRIPT_CARD_HEIGHT : COMPACT_CARD_HEIGHT
  return page([
    text({
      xPosition: DISPLAY_WIDTH - width - DISPLAY_MARGIN,
      yPosition: DISPLAY_HEIGHT - height - DISPLAY_MARGIN,
      width,
      height,
      borderWidth: 1,
      borderRadius: 10,
      paddingLength: transcript ? 9 : 8,
      containerID: 1,
      containerName: "live-transcript",
      content: transcriptContent,
      isEventCapture: 1,
    }),
  ])
}

export function buildTranscriptContent(
  content: string,
  thinkingFrame?: number,
): string {
  const thinking = thinkingFrame !== undefined
  const label = thinking
    ? `THINKING ${"·".repeat((thinkingFrame % 3) + 1)}`
    : "YOU"
  const transcript = tail(
    cleanText(content).replace(/\n/gu, " "),
    thinking ? 66 : 82,
  )
  return transcript ? `${label}  /  ${transcript}` : label
}

function resultText(item: string, index: number): string {
  const number = String(index + 1).padStart(2, "0")
  return `${number}  ${truncate(item, 52)}`
}

export function buildMessagePage(
  message: GlassesMessage,
): RebuildPageContainer {
  const label = message.kind.toUpperCase()
  const content = message.kind === "results"
    ? `${label}  /  ${truncate(message.intro, 44)}\n\n${message.items
      .map(resultText)
      .join("\n\n")}`
    : message.kind === "answer"
      ? truncate(message.body.replace(/\n+/gu, " "), 300)
      : `${label}\n\n${truncate(message.body.replace(/\n+/gu, " "), 280)}`

  return page([
    text({
      xPosition: DISPLAY_MARGIN,
      yPosition: 34,
      width: DISPLAY_WIDTH - DISPLAY_MARGIN * 2,
      height: 220,
      borderWidth: 1,
      borderRadius: 14,
      paddingLength: 14,
      containerID: 1,
      containerName: `${message.kind}-output`,
      content: `${content}\n\nTAP TO DISMISS`,
      isEventCapture: 1,
    }),
  ])
}
