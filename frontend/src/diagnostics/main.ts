import { AudioInputSource, OsEventTypeList } from "@evenrealities/even_hub_sdk"
import { supabase } from "../shared/api/supabase"
import { connectRealtimeSocket } from "../shared/api/client"
import { getEvenBridge, renderGlassesPage, resumeGlassesPage } from "../even/glasses-page-host"
import { buildCompactPage } from "../even/glasses-ui"
import { withTimeout } from "./promise-timeout"
import "./style.css"

const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T
const button = (id: string) => element<HTMLButtonElement>(id)
const phrases = /\b(don['’]t let me forget|remind me|remember|glasses|i need|need to|need|i plan to|i want to|i should|i prefer|repeat that|show that again)\b/giu
let token = ""
let socket: WebSocket | undefined
let unsubscribe: (() => void) | undefined
let running = false
let busy = false
let stopping = false
let frames = 0
let sentBytes = 0
let receivedBytes = 0
let startedAt = 0
let sequence = 0
let status = "Checking account…"
let stoppedResolve: (() => void) | undefined
const moonshine = new Map<string, string>()
const deepgram = new Map<string, string>()

function highlighted(target: HTMLElement, text: string) {
  let offset = 0
  for (const match of text.matchAll(phrases)) {
    target.append(document.createTextNode(text.slice(offset, match.index)))
    const mark = document.createElement("mark")
    mark.textContent = match[0]
    target.append(mark)
    offset = match.index + match[0].length
  }
  target.append(document.createTextNode(text.slice(offset)))
}

function renderTranscripts() {
  for (const [id, entries] of [["moonshine-transcripts", moonshine], ["server-transcripts", deepgram]] as const) {
    const list = element(id)
    if (!entries.size) {
      list.textContent = id === "moonshine-transcripts" ? "Waiting for speech…" : "Waiting for a keyword match…"
      continue
    }
    list.replaceChildren(...Array.from(entries.values(), text => {
      const row = document.createElement("li")
      const content = document.createElement("p")
      highlighted(content, text)
      row.append(content)
      return row
    }))
  }
  element("server-count").textContent = `${deepgram.size} received`
}

function render() {
  element("status").textContent = status
  button("start").disabled = !token || running || busy || stopping
  button("stop").disabled = !running || stopping
  button("clear-transcripts").disabled = running || busy || stopping
  element("elapsed").textContent = running ? `${Math.floor((Date.now()-startedAt)/1000)} seconds` : "Not running"
  const values = [`${frames} frames`, `${(sentBytes/32000).toFixed(1)}s`, `${(receivedBytes/32000).toFixed(1)}s`, "Server-side"]
  document.querySelectorAll("#metrics dd").forEach((node, i) => { node.textContent = values[i] })
}
function update(value: string) { status = value; render() }

async function stop(reason = "Stopped · transcripts retained") {
  if (stopping) return
  stopping = true
  running = false
  sequence += 1
  unsubscribe?.(); unsubscribe = undefined
  render()
  try {
    const bridge = await withTimeout(getEvenBridge(), 3000, "Bridge unavailable")
    await withTimeout(bridge.audioControl(false, AudioInputSource.Glasses), 5000, "Microphone stop unconfirmed")
  } catch { reason += " · close the app if the microphone remains active" }
  const current = socket
  if (current?.readyState === WebSocket.OPEN) {
    const drained = new Promise<void>(resolve => { stoppedResolve = resolve })
    current.send(JSON.stringify({ type: "ambient_stop" }))
    await withTimeout(drained, 30000, "Server flush timed out").catch(() => { reason += " · final results may be incomplete" })
  }
  socket = undefined
  current?.close()
  stoppedResolve = undefined
  stopping = false
  update(reason)
}

button("start").onclick = async () => {
  if (busy || running || stopping) return
  busy = true
  const attempt = ++sequence
  frames = sentBytes = receivedBytes = 0
  moonshine.clear(); deepgram.clear(); renderTranscripts()
  update("Connecting to server Moonshine…")
  try {
    const session = await supabase.auth.getSession()
    token = session.data.session?.access_token ?? ""
    const connected = await connectRealtimeSocket(token, { path: "/ws/moonshine" })
    socket = connected
    let readyResolve: (() => void) | undefined
    let readyReject: ((error: Error) => void) | undefined
    const ready = new Promise<void>((resolve, reject) => { readyResolve = resolve; readyReject = reject })
    connected.addEventListener("message", event => {
      let message: {type?: string; id?: string; text?: string; error?: string; received_bytes?: number}
      try { message = JSON.parse(String(event.data)) } catch { return }
      if (message.type === "ready") readyResolve?.()
      if (message.type === "audio_received") receivedBytes = message.received_bytes ?? receivedBytes
      if (message.type === "moonshine_transcript" || message.type === "deepgram_transcript") {
        if (message.text && message.id) {
          const entries = message.type === "moonshine_transcript" ? moonshine : deepgram
          entries.set(message.id, message.text)
          renderTranscripts()
        }
      }
      if (message.type === "keyword_detected") update("Keyword detected on server · sending clip to Deepgram")
      if (message.type === "deepgram_transcript") update("Deepgram result received · still streaming")
      if (message.error) { update(message.error); readyReject?.(new Error(message.error)) }
      if (message.type === "listening_stopped") {
        stoppedResolve?.()
        if (running && !stopping) void stop(message.error ?? "Server listening stopped")
      }
      render()
    })
    connected.addEventListener("close", () => {
      readyReject?.(new Error("Server connection closed"))
      stoppedResolve?.()
      if (socket === connected && running) void stop("Server disconnected · start a new test")
    })
    connected.send(JSON.stringify({ type: "ambient_start" }))
    await withTimeout(ready, 60000, "Server Moonshine did not become ready")
    const bridge = await withTimeout(getEvenBridge(), 10000, "Open inside Even with glasses connected")
    resumeGlassesPage()
    await withTimeout(renderGlassesPage(buildCompactPage("SERVER MOONSHINE TEST")), 10000, "Glasses page unavailable")
    if (attempt !== sequence) return
    startedAt = Date.now()
    running = true
    unsubscribe = bridge.onEvenHubEvent(event => {
      const kind = event.sysEvent?.eventType ?? event.listEvent?.eventType ?? event.textEvent?.eventType
      if (kind === OsEventTypeList.DOUBLE_CLICK_EVENT) { void stop(); return }
      const pcm = event.audioEvent?.audioPcm
      if (!running || !pcm?.length) return
      if (Date.now()-startedAt >= 7*60000) { void stop("Seven-minute test finished"); return }
      if (connected.readyState !== WebSocket.OPEN || connected.bufferedAmount > 2*32000) {
        void stop("Audio upload interrupted · start a new test"); return
      }
      // Every frame goes directly to Go, even when hidden: no local model,
      // AudioContext, VAD, keyword gate, or batching timer.
      const bytes = Uint8Array.from(pcm)
      if (bytes.byteLength % 2 !== 0) { void stop("Invalid glasses PCM frame"); return }
      try { connected.send(bytes); frames += 1; sentBytes += bytes.byteLength }
      catch { void stop("Audio upload failed") }
    })
    const started = await withTimeout(bridge.audioControl(true, AudioInputSource.Glasses), 15000, "Glasses microphone did not start")
    if (started === false) throw new Error("Glasses microphone did not start")
    if (attempt !== sequence) return
    update("Streaming all audio · Moonshine runs on the server")
  } catch (error) {
    socket?.close()
    await stop(String(error))
  } finally { busy = false; render() }
}

button("stop").onclick = () => void stop()
button("clear-transcripts").onclick = () => { moonshine.clear(); deepgram.clear(); renderTranscripts() }
button("reload").onclick = async () => { await stop(); location.reload() }
element<HTMLFormElement>("auth").onsubmit = async event => {
  event.preventDefault()
  button("sign-in").disabled = true
  try {
    const {data, error} = await supabase.auth.signInWithPassword({email:element<HTMLInputElement>("email").value,password:element<HTMLInputElement>("password").value})
    if (error || !data.session) throw new Error("Sign-in failed. Check your email and password.")
    token = data.session.access_token
    element("auth").hidden = true
    update("Ready · start the server Moonshine test")
  } catch (error) { element("auth-error").textContent = String(error) }
  finally { element<HTMLInputElement>("password").value = ""; button("sign-in").disabled = false }
}
// Backgrounding must not tear down capture. Repaint retained text on return.
document.addEventListener("visibilitychange", () => { renderTranscripts(); render() })
setInterval(render, 1000)
void supabase.auth.getSession().then(({data}) => {
  token = data.session?.access_token ?? ""
  element("auth").hidden = Boolean(token)
  update(token ? "Ready · start the server Moonshine test" : "Sign in to start")
})
renderTranscripts(); render()
