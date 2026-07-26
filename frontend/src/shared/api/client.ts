import { env } from "../config/env"

type TicketResponse = {
  ticket: string
}

type RealtimeConnectionOptions = {
  signal?: AbortSignal
  timeoutMs?: number
}

const DEFAULT_CONNECTION_TIMEOUT_MS = 10_000

function closeQuietly(socket: WebSocket) {
  try {
    socket.close()
  } catch {
    // The connection may already have been torn down by the browser.
  }
}

export async function connectRealtimeSocket(
  accessToken: string,
  options: RealtimeConnectionOptions = {},
): Promise<WebSocket> {
  const controller = new AbortController()
  const timeoutMs = options.timeoutMs ?? DEFAULT_CONNECTION_TIMEOUT_MS
  let timedOut = false

  const handleExternalAbort = () => {
    controller.abort()
  }
  if (options.signal?.aborted) {
    controller.abort()
  } else {
    options.signal?.addEventListener("abort", handleExternalAbort, { once: true })
  }

  const timeout = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)

  try {
    const response = await fetch(`${env.apiBaseUrl}/auth/ws-ticket`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${accessToken}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      }),
      signal: controller.signal,
    })
    if (!response.ok) {
      throw new Error(`WebSocket ticket request failed (${response.status})`)
    }

    const { ticket } = await response.json() as TicketResponse
    if (!ticket) {
      throw new Error("WebSocket ticket response was empty")
    }

    const url = new URL("/ws", env.apiBaseUrl)
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:"
    url.searchParams.set("ticket", ticket)

    return await new Promise<WebSocket>((resolve, reject) => {
      let socket: WebSocket
      try {
        socket = new WebSocket(url)
      } catch (error) {
        reject(error)
        return
      }

      const cleanup = () => {
        socket.removeEventListener("open", handleOpen)
        socket.removeEventListener("error", handleError)
        socket.removeEventListener("close", handleClose)
        controller.signal.removeEventListener("abort", handleAbort)
      }
      const fail = (error: Error) => {
        cleanup()
        closeQuietly(socket)
        reject(error)
      }
      const handleOpen = () => {
        cleanup()
        resolve(socket)
      }
      const handleError = () => {
        fail(new Error("WebSocket connection failed"))
      }
      const handleClose = () => {
        fail(new Error("WebSocket closed before connecting"))
      }
      const handleAbort = () => {
        fail(new Error(
          timedOut
            ? "WebSocket connection timed out"
            : "WebSocket connection cancelled",
        ))
      }

      socket.addEventListener("open", handleOpen, { once: true })
      socket.addEventListener("error", handleError, { once: true })
      socket.addEventListener("close", handleClose, { once: true })
      controller.signal.addEventListener("abort", handleAbort, { once: true })
      if (controller.signal.aborted) {
        handleAbort()
      }
    })
  } catch (error) {
    if (timedOut) {
      throw new Error("WebSocket connection timed out", { cause: error })
    }
    if (options.signal?.aborted) {
      throw new Error("WebSocket connection cancelled", { cause: error })
    }
    throw error
  } finally {
    clearTimeout(timeout)
    options.signal?.removeEventListener("abort", handleExternalAbort)
  }
}
