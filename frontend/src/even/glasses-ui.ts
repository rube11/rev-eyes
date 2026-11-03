import { RebuildPageContainer, TextContainerProperty } from "@evenrealities/even_hub_sdk"
import { paginateGlassesText, TEXT_COLUMNS, wrapGlassesText } from "./text-layout"

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

// Titles and controls have fixed space; replies still keep every page.
export function fitGlassesText(value: string, lines: number, columns = TEXT_COLUMNS): string {
  const wrapped = wrapGlassesText(cleanText(value), columns)
  if (wrapped.length <= lines) return wrapped.join("\n")
  const visible = wrapped.slice(0, lines)
  // Leave room for the ellipsis, including wide glyphs.
  const characters = Array.from(new Intl.Segmenter("en", { granularity: "grapheme" }).segment(visible[lines - 1]), part => part.segment)
  while (wrapGlassesText(characters.join("") + "…", columns).length > 1) characters.pop()
  visible[lines - 1] = characters.join("").trimEnd() + "…"
  return visible.join("\n")
}

export function buildThreadPage(title: string, detail: string, action: string): RebuildPageContainer {
  return buildMessagePage(presentGlassesMessage(`${fitGlassesText(title, 2)}\n\n${fitGlassesText(detail, 3)}`), action)
}

export function buildThreadPickerPage(
  threads: readonly { title: string; connectionId: string; status: string }[], index: number,
): RebuildPageContainer {
  const thread = threads[index]
  const start = Math.max(0, Math.min(index - 2, threads.length - 5))
  const rows = threads.slice(start, start + 5).map((item, offset) =>
    `${start + offset === index ? "-" : " "} ${fitGlassesText(item.title, 1, 9)}`)
  const state = thread.status.replaceAll("_", " ").trim() || "No status"
  return page([
    text({
      xPosition: MARGIN, yPosition: 20, width: 192, height: 208,
      containerID: 1, containerName: "thread-list", isEventCapture: 1,
      content: `Threads\n${rows.join("\n")}`,
    }),
    text({
      xPosition: 240, yPosition: 20, width: 312, height: 208,
      containerID: 3, containerName: "thread-detail", isEventCapture: 0,
      content: `${fitGlassesText(thread.title, 3, 18)}\n\n${fitGlassesText(thread.connectionId, 1, 18)}\n${fitGlassesText(`${index + 1}/${threads.length} · ${state}`, 1, 18)}`,
    }),
    status("Tap: open · Hold: back"),
  ])
}

function status(content: string, name = "message-status"): TextContainerProperty {
  return text({
    xPosition: MARGIN, yPosition: 244, width: CONTENT_WIDTH, height: 32,
    containerID: 2, containerName: name, content: fitGlassesText(content, 1), isEventCapture: 0,
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
    content: buildCompactContent(content), isEventCapture: 1,
  })])
}

export function buildCompactContent(content: string): string {
  return wrapGlassesText(cleanText(content)).slice(0, 2).join("\n")
}

export function buildTranscriptContent(content: string): string {
  // Only the live transcript is a moving window; saved answers are never cut.
  return wrapGlassesText(cleanText(content).replace(/\n/gu, " ")).slice(-2).join("\n") || " "
}

export function buildTranscriptPage(
  content: string,
  thinking = false,
  listeningStatus = "Listening",
): RebuildPageContainer {
  return page([
    text({
      xPosition: MARGIN, yPosition: 148, width: CONTENT_WIDTH, height: 80,
      containerID: 1, containerName: "live-transcript",
      content: buildTranscriptContent(content), isEventCapture: 1,
    }),
    status(thinking ? "Thinking" : listeningStatus, "speech-status"),
  ])
}

export function buildMessageStatus(message: GlassesMessage, action: string, pageIndex = 0): string {
  const count = glassesMessagePages(message).length
  const current = Math.max(0, Math.min(count - 1, pageIndex))
  if (count <= 1) return fitGlassesText(action, 1)
  return fitGlassesText(`${current + 1}/${count} · ${action}`, 1)
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
