// Conservative line budgets for the glasses' fixed font, not browser CSS.
export const TEXT_COLUMNS = 32
export const ANSWER_LINES = 6
const graphemes = new Intl.Segmenter("en", { granularity: "grapheme" })

function units(character: string): number {
  if (Array.from(character).some(part => part.codePointAt(0)! > 127)) return 2
  return /[MW@%]/u.test(character) ? 2 : 1
}

export function wrapGlassesText(value: string): string[] {
  const lines: string[] = []
  for (const paragraph of value.split("\n")) {
    let line = ""
    let width = 0
    for (const word of paragraph.trim().split(/\s+/u).filter(Boolean)) {
      const characters = Array.from(graphemes.segment(word), part => part.segment)
      const wordWidth = characters.reduce((sum, character) => sum + units(character), 0)
      if (line && width + 1 + wordWidth > TEXT_COLUMNS) {
        lines.push(line)
        line = ""
        width = 0
      }
      if (line) { line += " "; width += 1 }
      for (const character of characters) {
        const nextWidth = units(character)
        if (width + nextWidth > TEXT_COLUMNS) {
          lines.push(line)
          line = ""
          width = 0
        }
        line += character
        width += nextWidth
      }
    }
    lines.push(line)
  }
  return lines
}

export function paginateGlassesText(value: string): string[] {
  const lines = wrapGlassesText(value.trim())
  const pages: string[] = []
  for (let offset = 0; offset < lines.length; offset += ANSWER_LINES) {
    const content = lines.slice(offset, offset + ANSWER_LINES).join("\n").trim()
    if (content) pages.push(content)
  }
  return pages.length ? pages : [" "]
}
