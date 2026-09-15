import assert from "node:assert/strict"
import test from "node:test"
import { ANSWER_LINES, paginateGlassesText, TEXT_COLUMNS, wrapGlassesText } from "../src/even/text-layout.js"

test("long answers preserve all words across bounded pages", () => {
  const answer = Array.from({ length: 180 }, (_, i) => `word${i}`).join(" ")
  const pages = paginateGlassesText(answer)
  assert.ok(pages.length > 1)
  assert.equal(pages.join(" ").replace(/\s+/gu, " "), answer)
  for (const page of pages) {
    assert.ok(page.split("\n").length <= ANSWER_LINES)
    assert.ok(page.split("\n").every(line => line.length <= TEXT_COLUMNS))
  }
})

test("lists keep every item and the trailing explanation", () => {
  const answer = "Groceries\n\n" + Array.from({ length: 8 }, (_, i) => `- Item ${i}`).join("\n") + "\n\nRemember the closing advice."
  assert.equal(paginateGlassesText(answer).join(" ").replace(/\s+/gu, " "), answer.replace(/\s+/gu, " "))
})

test("wide glyphs and long unbroken strings fit without losing characters", () => {
  const word = "W".repeat(140)
  const lines = wrapGlassesText(word)
  assert.ok(lines.every(line => line.length <= TEXT_COLUMNS / 2))
  assert.equal(lines.join(""), word)
  const unicode = "你好👩‍💻".repeat(60)
  const wrapped = wrapGlassesText(unicode)
  assert.equal(wrapped.join(""), unicode)
  assert.ok(wrapped.every(line => !line.startsWith("‍") && !line.endsWith("‍")))
})

test("empty and boundary-length messages produce valid pages", () => {
  assert.deepEqual(paginateGlassesText(""), [" "])
  assert.equal(paginateGlassesText(("a".repeat(32) + "\n").repeat(6)).length, 1)
  assert.equal(paginateGlassesText(("a".repeat(32) + "\n").repeat(7)).length, 2)
})
