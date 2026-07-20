import { env } from '../config/env'

type TicketResponse = {
  ticket: string
}

export async function connectAudioSocket(accessToken: string): Promise<WebSocket> {
  const response = await fetch(`${env.apiBaseUrl}/auth/ws-ticket`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${accessToken}` },
  })
  if (!response.ok) {
    throw new Error(`WebSocket ticket request failed (${response.status})`)
  }

  const { ticket } = await response.json() as TicketResponse
  const url = new URL('/ws', env.apiBaseUrl)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.searchParams.set('ticket', ticket)

  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url)
    socket.addEventListener('open', () => resolve(socket), { once: true })
    socket.addEventListener(
      'error',
      () => reject(new Error('WebSocket connection failed')),
      { once: true },
    )
  })
}
