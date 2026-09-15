import { AudioInputSource, OsEventTypeList } from "@evenrealities/even_hub_sdk"

import { supabase } from "../shared/api/supabase"
import { connectRealtimeSocket } from "../shared/api/client"
import { CandidateAudioClient } from "../even/candidate-audio-client"
import { getEvenBridge, renderGlassesPage, resumeGlassesPage } from "../even/glasses-page-host"
import { buildCompactPage } from "../even/glasses-ui"
import type { MoonshineDiagnosticEvent } from "../even/moonshine-diagnostic-event"
import { parseRealtimeServerMessage } from "../even/realtime-protocol"
import { withTimeout } from "../even/promise-timeout"
import "./style.css"

const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T
const button = (id: string) => element<HTMLButtonElement>(id)
const checked = (id: string) => element<HTMLInputElement>(id).checked
const websocketOpen = 1
const maxServerTranscripts = 20
const wakePhrasePattern = /(don['’]t let me forget|remind me|remember|glasses|i need|need to|need|i plan to|i want to|i should|i prefer)/giu

let accessToken = ""
let client: CandidateAudioClient | undefined
let socket: WebSocket | undefined
let unsubscribe: (() => void) | undefined
let wanted = false
let busy = false
let stopping = false
let generation = 0
let startedAt = 0
let frames = 0
let lastAudioAt = 0
let lastVadAt = 0
let previousVad = 0
let recoveryAttempts = 0
let previousHealth = ""
let status = "Checking account…"
let liveTranscript = ""
const serverTranscripts: string[] = []

function appendHighlighted(container: HTMLElement, transcript: string) {
  container.replaceChildren()
  let offset = 0
  for (const match of transcript.matchAll(wakePhrasePattern)) {
    const index = match.index ?? 0
    if (index > offset) {
      container.append(document.createTextNode(transcript.slice(offset, index)))
    }
    const highlight = document.createElement("mark")
    highlight.textContent = match[0]
    container.append(highlight)
    offset = index + match[0].length
  }
  if (offset < transcript.length) {
    container.append(document.createTextNode(transcript.slice(offset)))
  }
}

function renderLiveTranscript() {
  const target = element("live-transcript")
  target.classList.toggle("transcript-empty", liveTranscript.length === 0)
  if (liveTranscript) {
    appendHighlighted(target, liveTranscript)
  } else {
    target.textContent = "Waiting for speech…"
  }
}

function renderServerTranscripts() {
  const list = element<HTMLOListElement>("server-transcripts")
  element("server-count").textContent = `${serverTranscripts.length} received`
  if (serverTranscripts.length === 0) {
    const row = document.createElement("li")
    row.className = "transcript-empty"
    row.textContent = "Waiting for an accepted wake phrase…"
    list.replaceChildren(row)
    return
  }
  const rows = serverTranscripts.map((transcript, index) => {
    const row = document.createElement("li")
    const label = document.createElement("small")
    label.textContent = `SERVER TRANSCRIPT ${String(index + 1).padStart(2, "0")}`
    const content = document.createElement("p")
    appendHighlighted(content, transcript)
    row.append(label, content)
    return row
  })
  list.replaceChildren(...rows)
  rows.at(-1)?.scrollIntoView({ behavior: "smooth", block: "nearest" })
}

function updateStatus(value: string) {
  status = value
  render()
}

function render() {
  element("status").textContent = status
  button("start").disabled = !accessToken || wanted || busy || stopping
  button("stop").disabled = !wanted || stopping
  button("retry").disabled = !wanted || busy || stopping
  button("clear-transcripts").disabled = wanted || busy || stopping
  element("elapsed").textContent = wanted
    ? `${Math.floor((Date.now() - startedAt) / 1_000)} seconds`
    : "Not running"
  const state = client?.snapshot()
  const values = [
    `${frames} frames${lastAudioAt ? ` · last ${Math.floor((Date.now() - lastAudioAt) / 1_000)}s ago` : ""}`,
    state?.adapterContext ?? "—",
    state?.inferenceHealthy === false
      ? "Stalled · reload required"
      : state?.moonshineContext ?? "—",
    `${state?.speechStarts ?? 0} / ${state?.commits ?? 0} (this audio run)`,
  ]
  document.querySelectorAll("#metrics dd").forEach((node, index) => {
    node.textContent = values[index]
  })
}

function closeSocket() {
  const current = socket
  socket = undefined
  try {
    current?.close()
  } catch {
    // The browser may already have closed it.
  }
}

function handleServerMessage(event: MessageEvent<unknown>) {
  const message = parseRealtimeServerMessage(event.data)
  if (!message) {
    return
  }
  if (message.type === "user_transcript" && message.text) {
    serverTranscripts.push(message.text)
    serverTranscripts.splice(0, Math.max(0, serverTranscripts.length - maxServerTranscripts))
    renderServerTranscripts()
    updateStatus("Server transcript received · still listening")
    return
  }
  if (
    message.type === "assistant_done" ||
    message.type === "assistant_response" ||
    message.type === "assistant_repeat"
  ) {
    client?.complete(message.id)
  }
}

async function connectServer() {
  if (socket?.readyState === websocketOpen) {
    return
  }
  if (!accessToken) {
    throw new Error("Sign in before starting the server transcript test")
  }
  closeSocket()
  updateStatus("Connecting to Go server…")
  const connected = await connectRealtimeSocket(accessToken)
  connected.addEventListener("message", handleServerMessage)
  connected.addEventListener("close", () => {
    if (socket !== connected) {
      return
    }
    socket = undefined
    if (wanted) {
      updateStatus("Server disconnected · stop and retry")
    }
  })
  socket = connected
}

async function stop(reason: string) {
  wanted = false
  generation += 1
  if (stopping) {
    return
  }
  stopping = true
  updateStatus("Stopping microphone")
  try {
    const confirmed = client
      ? await withTimeout(client.stopCapture(false), 5_000, "Microphone stop timed out")
      : true
    updateStatus(
      confirmed
        ? reason.includes("inference")
          ? "Inference timed out · reload required"
          : "Stopped · transcripts retained"
        : "Stop unconfirmed — close this app in Even",
    )
  } catch {
    updateStatus("Stop unconfirmed — close this app in Even")
  } finally {
    closeSocket()
    stopping = false
    render()
  }
}

async function setup() {
  const bridge = await withTimeout(
    getEvenBridge(),
    10_000,
    "Open this test inside the Even app with your glasses connected",
  )
  resumeGlassesPage()
  await withTimeout(
    renderGlassesPage(buildCompactPage("SERVER TRANSCRIPT TEST")),
    10_000,
    "Glasses page did not start",
  )
  if (client) {
    return
  }
  client = new CandidateAudioClient({
    candidateAudioEnabled: true,
    moonshineEnabled: true,
    debugTranscripts: true,
    forwardDiagnostics: false,
    getSocket: () => socket,
    device: {
      start: () => bridge.audioControl(true, AudioInputSource.Glasses),
      stop: () => bridge.audioControl(false, AudioInputSource.Glasses),
    },
    onCandidateSent: () => updateStatus("Audio sent · waiting for server transcript…"),
    onCandidateFinalized: (event) => {
      if (!event.submitted) {
        updateStatus("Speech heard · no wake phrase selected")
      }
    },
    onDiagnostic: (event: MoonshineDiagnosticEvent) => {
      if (event.event !== "transcript") {
        return
      }
      liveTranscript = event.text
      renderLiveTranscript()
    },
  })
  unsubscribe = bridge.onEvenHubEvent((event) => {
    const kind =
      event.sysEvent?.eventType ??
      event.listEvent?.eventType ??
      event.textEvent?.eventType
    if (kind === OsEventTypeList.DOUBLE_CLICK_EVENT) {
      void stop("glasses app exit")
      return
    }
    const pcm = event.audioEvent?.audioPcm
    if (!pcm || !wanted) {
      return
    }
    if (Date.now() - startedAt >= 7 * 60_000) {
      void stop("7 minute limit")
      return
    }
    frames += 1
    lastAudioAt = Date.now()
    if (client?.captureRunning) {
      client.push(Uint8Array.from(pcm))
    }
  })
}

button("start").onclick = async () => {
  if (busy || wanted || stopping || !accessToken) {
    return
  }
  wanted = true
  busy = true
  const attempt = ++generation
  recoveryAttempts = 0
  frames = 0
  lastAudioAt = 0
  liveTranscript = ""
  renderLiveTranscript()
  startedAt = Date.now()
  updateStatus("Connecting glasses, server, and local model…")
  try {
    await connectServer()
    await setup()
    if (!wanted || generation !== attempt) {
      return
    }
    if (!await withTimeout(client!.prepare(), 120_000, "Model loading timed out; reload before retrying")) {
      throw new Error("Model unavailable — retry, or reload if inference stalled")
    }
    if (!wanted || generation !== attempt) {
      return
    }
    const started = await withTimeout(
      client!.startCapture(() => wanted && generation === attempt),
      15_000,
      "Audio start timed out; reload before retrying",
    )
    if (!wanted || generation !== attempt) {
      return
    }
    if (!started) {
      throw new Error("Audio could not start. Retry or reload the app.")
    }
    startedAt = Date.now()
    lastVadAt = Date.now()
    previousVad = 0
    updateStatus("Listening · speak a highlighted wake phrase")
  } catch (error) {
    await stop("start failed")
    updateStatus(String(error))
  } finally {
    busy = false
    render()
  }
}

async function recover(reason: string) {
  if (busy || !wanted || !client) {
    return
  }
  busy = true
  const attempt = generation
  updateStatus(`Recovering audio · ${reason}`)
  try {
    await connectServer()
    const stopped = await withTimeout(client.stopCapture(false), 5_000, "Microphone stop timed out")
    if (!stopped) {
      throw new Error("Microphone stop unconfirmed; close this app in Even")
    }
    if (!wanted || generation !== attempt) {
      return
    }
    if (!await withTimeout(client.startCapture(() => wanted && generation === attempt), 15_000, "Audio recovery timed out")) {
      throw new Error("Recovery failed; reload if model is stalled")
    }
    previousVad = 0
    lastVadAt = Date.now()
    updateStatus("Listening · audio recovered")
  } catch (error) {
    await stop("recovery failed")
    updateStatus(String(error))
  } finally {
    busy = false
    render()
  }
}

button("retry").onclick = () => void recover("manual retry")
button("stop").onclick = () => void stop("user")
button("clear-transcripts").onclick = () => {
  liveTranscript = ""
  serverTranscripts.splice(0)
  renderLiveTranscript()
  renderServerTranscripts()
}
button("reload").onclick = async () => {
  await stop("reload")
  location.reload()
}

element<HTMLFormElement>("auth").onsubmit = async (event) => {
  event.preventDefault()
  button("sign-in").disabled = true
  element("auth-error").textContent = ""
  const email = element<HTMLInputElement>("email").value
  const password = element<HTMLInputElement>("password").value
  const { data, error } = await supabase.auth.signInWithPassword({ email, password })
  element<HTMLInputElement>("password").value = ""
  button("sign-in").disabled = false
  if (error || !data.session) {
    element("auth-error").textContent = "Sign-in failed. Check your email and password."
    return
  }
  accessToken = data.session.access_token
  element("auth").hidden = true
  updateStatus("Ready · start the server transcript test")
}

window.addEventListener("pagehide", () => {
  wanted = false
  generation += 1
  closeSocket()
  client?.dispose()
  client = undefined
  unsubscribe?.()
  unsubscribe = undefined
})

setInterval(() => {
  const now = Date.now()
  if (!wanted || busy) {
    render()
    return
  }
  const state = client?.snapshot()
  if ((state?.vadFrames ?? 0) !== previousVad) {
    lastVadAt = now
    previousVad = state?.vadFrames ?? 0
  }
  const health = state?.inferenceHealthy === false
    ? "Inference stalled — reload required"
    : now - (lastAudioAt || startedAt) > 5_000
      ? "No recent glasses audio"
      : state?.adapterContext !== "running" || state?.moonshineContext !== "running"
        ? "Audio context interrupted"
        : now - lastVadAt > 5_000
          ? "Speech detector not advancing"
          : socket?.readyState !== websocketOpen
            ? "Server disconnected"
            : ""
  if (health !== previousHealth) {
    updateStatus(health || "Listening · health restored")
    previousHealth = health
  }
  if (now - startedAt >= 7 * 60_000) {
    void stop("7 minute limit")
  } else if (state?.inferenceHealthy === false) {
    void stop("inference stalled; reload required")
  } else if (health && checked("recover") && recoveryAttempts < 2) {
    recoveryAttempts += 1
    void recover(`automatic ${recoveryAttempts}/2: ${health}`)
  }
  render()
}, 2_000)

void supabase.auth.getSession().then(({ data }) => {
  accessToken = data.session?.access_token ?? ""
  element("auth").hidden = Boolean(accessToken)
  updateStatus(
    accessToken
      ? "Ready · start the server transcript test"
      : "Sign in to connect to the Go server",
  )
})

renderLiveTranscript()
renderServerTranscripts()
render()
