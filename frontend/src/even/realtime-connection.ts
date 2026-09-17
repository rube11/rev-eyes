import { connectRealtimeSocket } from "../shared/api/client"
import { closeSocketQuietly, closeUnadoptedSocket, reconnectDelay, socketIsOpen } from "./realtime-socket"

type SocketBinding = {
  socket: WebSocket
  message: (event: MessageEvent<unknown>) => void
  close: () => void
}

type Options = {
  enqueue: (transition: () => Promise<void>) => Promise<void>
  connected: (socket: WebSocket) => Promise<void>
  unavailable: () => Promise<void>
}

// Owns socket adoption, listeners, and reconnect attempts. UI transitions stay
// serialized by the glasses runtime; a late connection can never be adopted
// after teardown or replace a newer attempt.
export class RealtimeConnection {
  private active = true
  private socket: WebSocket | undefined
  private binding: SocketBinding | undefined
  private timer: ReturnType<typeof setTimeout> | undefined
  private attempt = 0
  private connecting = false
  private generation = 0
  private controller: AbortController | undefined

  private readonly accessToken: string
  private readonly options: Options

  constructor(accessToken: string, options: Options) {
    this.accessToken = accessToken
    this.options = options
  }

  get current(): WebSocket | undefined { return this.socket }

  start(): void { this.schedule(true) }

  reconnect(): void {
    if (!this.active || this.connecting || socketIsOpen(this.socket)) return
    this.clearTimer()
    this.schedule(true)
  }

  schedule(immediate = false): void {
    if (!this.active || this.connecting || socketIsOpen(this.socket) || this.timer !== undefined) return
    const delay = immediate ? 0 : reconnectDelay(this.attempt)
    if (!immediate) this.attempt += 1
    this.timer = setTimeout(() => {
      this.timer = undefined
      this.connect()
    }, delay)
  }

  adopt(socket: WebSocket): void {
    this.clearTimer()
    this.attempt = 0
    if (this.socket && this.socket !== socket) {
      this.unbind()
      closeSocketQuietly(this.socket)
    }
    this.socket = socket
  }

  bind(socket: WebSocket, message: SocketBinding["message"], close: () => void): void {
    this.binding = { socket, message, close }
    socket.addEventListener("message", message)
    socket.addEventListener("close", close)
  }

  release(socket: WebSocket): void {
    if (this.socket !== socket) return
    this.unbind()
    this.socket = undefined
  }

  dispose(): void {
    this.active = false
    this.generation += 1
    this.controller?.abort()
    this.controller = undefined
    this.connecting = false
    this.clearTimer()
    this.unbind()
    const socket = this.socket
    this.socket = undefined
    closeSocketQuietly(socket)
  }

  private unbind(): void {
    if (!this.binding) return
    const { socket, message, close } = this.binding
    socket.removeEventListener("message", message)
    socket.removeEventListener("close", close)
    this.binding = undefined
  }

  private clearTimer(): void {
    if (this.timer !== undefined) clearTimeout(this.timer)
    this.timer = undefined
  }

  private connect(): void {
    if (!this.active || this.connecting || socketIsOpen(this.socket)) return
    this.connecting = true
    const generation = ++this.generation
    const controller = new AbortController()
    this.controller = controller
    void (async () => {
      try {
        const socket = await connectRealtimeSocket(this.accessToken, {
          signal: controller.signal,
          timeoutMs: 10_000,
        })
        if (!this.active || generation !== this.generation) {
          closeSocketQuietly(socket)
          return
        }
        this.connecting = false
        this.controller = undefined
        await this.options.enqueue(async () => {
          if (generation !== this.generation) return
          await this.options.connected(socket)
        })
        closeUnadoptedSocket(socket, this.socket)
      } catch {
        if (!this.active || generation !== this.generation) return
        this.connecting = false
        this.controller = undefined
        await this.options.enqueue(this.options.unavailable)
        this.schedule()
      }
    })()
  }
}
