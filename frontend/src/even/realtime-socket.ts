const WEBSOCKET_OPEN_STATE = 1
const RECONNECT_BASE_DELAY_MS = 500
const RECONNECT_MAX_DELAY_MS = 10_000

export type RealtimeSocket = {
  readonly readyState: number
  send(data: string | ArrayBuffer): void
  close(): void
}

export function socketIsOpen(
  socket: RealtimeSocket | undefined,
): socket is RealtimeSocket {
  return socket?.readyState === WEBSOCKET_OPEN_STATE
}

export function closeSocketQuietly(
  socket: Pick<RealtimeSocket, "close"> | undefined,
): void {
  if (!socket) {
    return
  }
  try {
    socket.close()
  } catch {
    // The browser may already have finalized the socket.
  }
}

export function closeUnadoptedSocket(
  opened: RealtimeSocket,
  adopted: RealtimeSocket | undefined,
): void {
  if (opened !== adopted) {
    closeSocketQuietly(opened)
  }
}

export function safeSend(
  socket: RealtimeSocket | undefined,
  data: string | ArrayBuffer,
): boolean {
  if (!socketIsOpen(socket)) {
    return false
  }
  try {
    socket.send(data)
    return true
  } catch {
    return false
  }
}

export function safeSendJson(
  socket: RealtimeSocket | undefined,
  value: object,
): boolean {
  try {
    return safeSend(socket, JSON.stringify(value))
  } catch {
    return false
  }
}

export function reconnectDelay(
  attempt: number,
  random: () => number = Math.random,
): number {
  const exponential = Math.min(
    RECONNECT_MAX_DELAY_MS,
    RECONNECT_BASE_DELAY_MS * 2 ** Math.min(Math.max(0, attempt), 5),
  )
  const jitter = 0.75 + random() * 0.5
  return Math.min(
    RECONNECT_MAX_DELAY_MS,
    Math.round(exponential * jitter),
  )
}
