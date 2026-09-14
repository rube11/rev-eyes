import { RebuildPageContainer, TextContainerProperty } from "@evenrealities/even_hub_sdk"
import { paginateGlassesText, wrapGlassesText } from "./text-layout"

const DISPLAY_WIDTH = 576
const DISPLAY_HEIGHT = 288
const MARGIN = 24
const CONTENT_WIDTH = DISPLAY_WIDTH - MARGIN * 2

export type GlassesMessage = {
  kind: "answer" | "results" | "reminder" | "update"
  body: string
}

function cleanText(value: string): string {
  return value.replace(/\r\n?/gu, "\n")
    .replace(/\[(.+?)\]\(https?:\/\/[^)]+\)/giu, "$1")
    .replace(/\*\*|__/gu, "")
    .replace(/`([^`]+)`/gu, "$1")
    .split("\n")
    .map(line => line.replace(/^#{1,6}\s+/u, "").replace(/\s+/gu, " ").trim())
    .join("\n").replace(/\n{3,}/gu, "\n\n").trim()
}

export function presentGlassesMessage(value: string): GlassesMessage {
  const body = cleanText(value)
  const kind = /^reminder\s*:/iu.test(body) ? "reminder"
    : /^possible update on\s+/iu.test(body) ? "update"
      : body.split("\n").filter(line => /^(?:[-*•]|\d{1,2}[.)])\s+/u.test(line)).length >= 2
        ? "results" : "answer"
  // Keep the entire answer, including every list item and its closing paragraph.
  return { kind, body }
}

export function glassesMessagePages(message: GlassesMessage): string[] {
  return paginateGlassesText(message.body)
}

function text(data: Partial<TextContainerProperty>): TextContainerProperty {
  return new TextContainerProperty({ borderWidth: 0, paddingLength: 0, ...data })
}

function page(containers: TextContainerProperty[]): RebuildPageContainer {
  return new RebuildPageContainer({ containerTotalNum: containers.length, textObject: containers })
}

function status(content: string, name = "message-status"): TextContainerProperty {
  return text({
    xPosition: MARGIN, yPosition: 244, width: CONTENT_WIDTH, height: 32,
    containerID: 2, containerName: name, content, isEventCapture: 0,
  })
}

export function buildSleepPage(): RebuildPageContainer {
  return page([text({
    xPosition: 0, yPosition: 0, width: DISPLAY_WIDTH, height: DISPLAY_HEIGHT,
    containerID: 1, containerName: "sleep-wake", content: " ", isEventCapture: 1,
  })])
}

export function buildCompactPage(content: string): RebuildPageContainer {
  return page([text({
    xPosition: MARGIN, yPosition: 228, width: CONTENT_WIDTH, height: 56,
    containerID: 1, containerName: "compact-control",
    content: wrapGlassesText(cleanText(content)).slice(0, 2).join("\n"), isEventCapture: 1,
  })])
}

export function buildTranscriptContent(content: string): string {
  // Only the live transcript is a moving window; saved answers are never cut.
  return wrapGlassesText(cleanText(content).replace(/\n/gu, " ")).slice(-2).join("\n") || " "
}

export function buildTranscriptPage(content: string, thinking = false): RebuildPageContainer {
  return page([
    text({
      xPosition: MARGIN, yPosition: 148, width: CONTENT_WIDTH, height: 80,
      containerID: 1, containerName: "live-transcript",
      content: buildTranscriptContent(content), isEventCapture: 1,
    }),
    status(thinking ? "Thinking" : "Listening", "transcript-status"),
  ])
}

export function buildMessageStatus(message: GlassesMessage, action: string, pageIndex = 0): string {
  const count = glassesMessagePages(message).length
  const current = Math.max(0, Math.min(count - 1, pageIndex))
  return count > 1 ? `${current + 1}/${count} · Scroll · ${action}` : action
}

export function buildMessagePage(
  message: GlassesMessage, action = "Tap to dismiss", pageIndex = 0,
): RebuildPageContainer {
  const pages = glassesMessagePages(message)
  const index = Math.max(0, Math.min(pages.length - 1, pageIndex))
  return page([
    text({
      xPosition: MARGIN, yPosition: 20, width: CONTENT_WIDTH, height: 208,
      containerID: 1, containerName: `${message.kind}-output`,
      content: pages[index], isEventCapture: 1,
    }),
    status(buildMessageStatus(message, action, index)),
  ])
}
