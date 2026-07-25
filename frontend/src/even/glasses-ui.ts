import {
  RebuildPageContainer,
  TextContainerProperty,
} from "@evenrealities/even_hub_sdk"

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

function chrome(status: string, action: string): TextContainerProperty[] {
  return [
    text({
      xPosition: 24,
      yPosition: 12,
      width: 180,
      height: 22,
      containerID: 1,
      containerName: "brand",
      content: "REV EYES",
      isEventCapture: 0,
    }),
    text({
      xPosition: 438,
      yPosition: 12,
      width: 114,
      height: 22,
      containerID: 2,
      containerName: "status",
      content: status.toUpperCase(),
      isEventCapture: 0,
    }),
    text({
      xPosition: 24,
      yPosition: 252,
      width: 528,
      height: 24,
      containerID: 3,
      containerName: "voice-control",
      content: action,
      isEventCapture: 1,
    }),
  ]
}

export function buildSystemPage(
  status: string,
  title: string,
  message: string,
  action: string,
): RebuildPageContainer {
  return page([
    ...chrome(status, action),
    text({
      xPosition: 24,
      yPosition: 58,
      width: 528,
      height: 174,
      containerID: 4,
      containerName: "system-message",
      content: `${title.toUpperCase()}\n\n${truncate(cleanText(message), 220)}`,
      isEventCapture: 0,
    }),
  ])
}

function resultText(item: string, index: number): string {
  const parts = item.split(/\s(?:—|–|-|·|\|)\s/u, 2)
  const number = String(index + 1).padStart(2, "0")
  return parts.length === 2
    ? `${number}  ${truncate(parts[0], 42)}\n${truncate(parts[1], 62)}`
    : `${number}  ${truncate(item, 82)}`
}

function buildResultsPage(
  message: Extract<GlassesMessage, { kind: "results" }>,
  status: string,
): RebuildPageContainer {
  const cardHeight = Math.floor((166 - 6 * (message.items.length - 1)) / message.items.length)
  return page([
    ...chrome(status, "Tap to talk"),
    text({
      xPosition: 24,
      yPosition: 43,
      width: 528,
      height: 22,
      containerID: 4,
      containerName: "results-label",
      content: `RESULTS  /  ${message.intro}`,
      isEventCapture: 0,
    }),
    ...message.items.map((item, index) => text({
      xPosition: 24,
      yPosition: 72 + index * (cardHeight + 6),
      width: 528,
      height: cardHeight,
      borderWidth: 1,
      borderRadius: 10,
      paddingLength: 9,
      containerID: 5 + index,
      containerName: `result-${index + 1}`,
      content: resultText(item, index),
      isEventCapture: 0,
    })),
  ])
}

export function buildMessagePage(
  message: GlassesMessage,
  status: string,
): RebuildPageContainer {
  if (message.kind === "results") {
    return buildResultsPage(message, status)
  }

  const popup = message.kind === "reminder" || message.kind === "update"
  const label = message.kind === "answer" ? "ANSWER" : message.kind.toUpperCase()
  return page([
    ...chrome(status, popup ? "Tap to dismiss" : "Tap to talk"),
    text({
      xPosition: popup ? 42 : 24,
      yPosition: 54,
      width: popup ? 492 : 528,
      height: 176,
      borderWidth: popup ? (message.kind === "reminder" ? 2 : 1) : 0,
      borderRadius: popup ? 14 : 0,
      paddingLength: popup ? 16 : 0,
      containerID: 4,
      containerName: popup ? `${message.kind}-popup` : "answer",
      content: `${label}\n\n${message.body}`,
      isEventCapture: 0,
    }),
  ])
}
