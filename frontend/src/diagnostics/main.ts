import { AudioInputSource, OsEventTypeList } from "@evenrealities/even_hub_sdk"
import { CandidateAudioClient } from "../even/candidate-audio-client"
import { getEvenBridge, renderGlassesPage, resumeGlassesPage } from "../even/glasses-page-host"
import { buildCompactPage } from "../even/glasses-ui"
import { withTimeout } from "../even/promise-timeout"
import { DiagnosticLog } from "./log"
import "./style.css"

const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T
const button = (id: string) => element<HTMLButtonElement>(id)
const checked = (id: string) => element<HTMLInputElement>(id).checked
let storage: Storage | undefined
try { storage = localStorage } catch { /* In-memory diagnostics still work. */ }
const log = new DiagnosticLog(storage)
let client: CandidateAudioClient | undefined
let unsubscribe: (() => void) | undefined
let wanted = false
let busy = false
let stopping = false
let generation = 0
let startedAt = 0
let lastTick = Date.now()
let frames = 0
let lastAudioAt = 0
let lastVadAt = 0
let previousVad = 0
let recoveryAttempts = 0
let previousHealth = ""
let status = "Ready to test"

function record(event: string, data: Record<string, unknown> = {}) {
  log.add(event, { visibility: document.visibilityState, ...data })
}
function updateStatus(value: string) { status = value; render() }
function render() {
  element("status").textContent = status
  element("storage").textContent = log.persistent
    ? "Results saved on this phone · latest 500 events"
    : "Storage unavailable — export before closing; results are only in memory"
  button("start").disabled = wanted || busy || stopping
  button("stop").disabled = !wanted || stopping
  button("retry").disabled = !wanted || busy || stopping
  button("clear").disabled = wanted || busy || stopping
  element("elapsed").textContent = wanted ? `${Math.floor((Date.now() - startedAt) / 1000)} seconds` : "Not running"
  const state = client?.snapshot()
  const values = [
    `${frames} frames${lastAudioAt ? ` · last ${Math.floor((Date.now() - lastAudioAt) / 1000)}s ago` : ""}`,
    state?.adapterContext ?? "—",
    state?.inferenceHealthy === false ? "Stalled · reload required" : state?.moonshineContext ?? "—",
    `${state?.speechStarts ?? 0} / ${state?.commits ?? 0} (this audio run)`,
  ]
  document.querySelectorAll("#metrics dd").forEach((node, i) => { node.textContent = values[i] })
  const rows = log.entries.slice(-30).reverse().map(entry => {
    const row = document.createElement("li")
    const time = document.createElement("small")
    time.textContent = new Date(entry.at).toLocaleTimeString()
    const title = document.createElement("div")
    title.textContent = entry.event
    const detail = document.createElement("code")
    detail.textContent = JSON.stringify(entry.data)
    row.append(time, title, detail)
    return row
  })
  element("timeline").replaceChildren(...rows)
}

async function stop(reason: string) {
  wanted = false
  generation += 1
  if (stopping) return
  stopping = true
  record("stop requested", { reason })
  updateStatus("Stopping microphone")
  try {
    const confirmed = client ? await withTimeout(client.stopCapture(false), 5_000, "Microphone stop timed out") : true
    record("stop result", { confirmed })
    updateStatus(confirmed
      ? reason.includes("inference") ? "Inference timed out · reload required" : "Stopped · results retained"
      : "Stop unconfirmed — close this app in Even")
  } catch (error) {
    record("stop error", { message: String(error) })
    updateStatus("Stop unconfirmed — close this app in Even")
  } finally {
    stopping = false
    render()
  }
}

async function setup() {
  const bridge = await withTimeout(getEvenBridge(), 10_000, "Open this test inside the Even app with your glasses connected")
  resumeGlassesPage()
  await withTimeout(renderGlassesPage(buildCompactPage("EYES LISTENING TEST")), 10_000, "Glasses page did not start")
  if (client) return
  client = new CandidateAudioClient({
    candidateAudioEnabled: true,
    moonshineEnabled: true,
    debugTranscripts: true,
    forwardDiagnostics: false,
    getSocket: () => undefined, // Deliberately independent of server/auth/network.
    device: {
      start: () => bridge.audioControl(true, AudioInputSource.Glasses),
      stop: () => bridge.audioControl(false, AudioInputSource.Glasses),
    },
    onDiagnostic: event => {
      if (event.event === "transcript") {
        const marker = event.text.match(/\btest\s+(one|two|three|four|1|2|3|4)\b/i)?.[0]
        record("transcript", {
          kind: event.kind, marker: marker ?? null, characters: event.text.length,
          ...(checked("transcripts") ? { text: event.text.slice(0, 400) } : {}),
        })
      } else record(event.event, { ...event })
    },
  })
  unsubscribe = bridge.onEvenHubEvent(event => {
    const kind = event.sysEvent?.eventType ?? event.listEvent?.eventType ?? event.textEvent?.eventType
    if (kind === OsEventTypeList.DOUBLE_CLICK_EVENT) {
      void stop("glasses app exit")
      return
    }
    const pcm = event.audioEvent?.audioPcm
    if (!pcm || !wanted) return
    if (Date.now() - startedAt >= 7 * 60_000) { void stop("7 minute limit"); return }
    frames += 1
    lastAudioAt = Date.now()
    if (client?.captureRunning) client.push(Uint8Array.from(pcm))
  })
}

button("start").onclick = async () => {
  if (busy || wanted || stopping) return
  wanted = true
  busy = true
  const attempt = ++generation
  recoveryAttempts = 0
  frames = 0
  lastAudioAt = 0
  startedAt = Date.now()
  record("test requested", { build: "0.1.0", autoRecovery: checked("recover"), saveText: checked("transcripts") })
  updateStatus("Connecting glasses and loading model…")
  try {
    await setup()
    if (!wanted || generation !== attempt) return
    if (!await withTimeout(client!.prepare(), 120_000, "Model loading timed out; reload before retrying")) throw new Error("Model unavailable — retry, or reload if inference stalled")
    if (!wanted || generation !== attempt) return
    const started = await withTimeout(client!.startCapture(() => wanted && generation === attempt), 15_000, "Audio start timed out; reload before retrying")
    if (!wanted || generation !== attempt) return
    if (!started) throw new Error("Audio could not start. Retry or reload the app.")
    startedAt = Date.now()
    lastVadAt = Date.now()
    previousVad = 0
    record("listening started")
    updateStatus("Listening · say test one, then lock your phone")
  } catch (error) {
    record("start error", { message: String(error) })
    await stop("start failed")
    updateStatus(String(error))
  } finally { busy = false; render() }
}

async function recover(reason: string) {
  if (busy || !wanted || !client) return
  busy = true
  const attempt = generation
  record("recovery requested", { reason })
  updateStatus("Recovering audio…")
  try {
    const stopped = await withTimeout(client.stopCapture(false), 5_000, "Microphone stop timed out")
    if (!stopped) throw new Error("Microphone stop unconfirmed; close this app in Even")
    if (!wanted || generation !== attempt) return
    if (!await withTimeout(client.startCapture(() => wanted && generation === attempt), 15_000, "Audio recovery timed out")) throw new Error("Recovery failed; reload if model is stalled")
    previousVad = 0
    lastVadAt = Date.now()
    record("recovery completed")
    updateStatus("Listening · recovered (see timeline)")
  } catch (error) {
    record("recovery failed", { message: String(error) })
    await stop("recovery failed")
    updateStatus(String(error))
  } finally { busy = false; render() }
}
button("retry").onclick = () => { void recover("manual") }
button("stop").onclick = () => { void stop("user") }
button("clear").onclick = () => { log.clear(); render() }
button("export").onclick = () => {
  const report = element<HTMLTextAreaElement>("report")
  report.hidden = false
  report.value = JSON.stringify({ build: "0.1.0", exportedAt: new Date().toISOString(), events: log.entries }, null, 2)
  report.focus()
  report.select()
}
button("reload").onclick = async () => { await stop("reload"); location.reload() }
document.addEventListener("visibilitychange", () => { record("visibility changed"); render() })
window.addEventListener("pagehide", () => {
  record("page hidden/unloaded")
  wanted = false
  generation += 1
  client?.dispose()
  client = undefined
  unsubscribe?.()
  unsubscribe = undefined
})
window.addEventListener("online", () => record("network online"))
window.addEventListener("offline", () => record("network offline; local listening unchanged"))
setInterval(() => {
  const now = Date.now()
  if (wanted && now - lastTick > 5_000) record("JavaScript scheduling gap", { milliseconds: now - lastTick })
  lastTick = now
  if (wanted && !busy) {
    const state = client?.snapshot()
    record("sample", { frames, lastAudioAt, ...state })
    if ((state?.vadFrames ?? 0) !== previousVad) { lastVadAt = now; previousVad = state?.vadFrames ?? 0 }
    const health = state?.inferenceHealthy === false ? "Inference stalled — reload required"
      : now - (lastAudioAt || startedAt) > 5_000 ? "No recent glasses audio"
      : state?.adapterContext !== "running" || state?.moonshineContext !== "running" ? "Audio context interrupted"
      : now - lastVadAt > 5_000 ? "Speech detector not advancing" : ""
    if (health !== previousHealth) {
      if (health) { record("health warning", { message: health }); updateStatus(health) }
      else if (previousHealth) { record("health restored"); updateStatus("Listening · health restored") }
      previousHealth = health
    }
    if (now - startedAt >= 7 * 60_000) void stop("7 minute limit")
    else if (state?.inferenceHealthy === false) void stop("inference stalled; reload required")
    else if (health && checked("recover") && recoveryAttempts < 2) {
      recoveryAttempts += 1
      void recover(`automatic ${recoveryAttempts}/2: ${health}`)
    }
  }
  render()
}, 2_000)
record("diagnostics opened", { previousHistory: log.entries.length > 0 })
render()
