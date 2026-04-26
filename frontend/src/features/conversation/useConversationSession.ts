import { useState } from 'react'

import type { ConversationStatus } from './conversationTypes'

export function useConversationSession() {
  const [status] = useState<ConversationStatus>('idle')

  return {
    status,
  }
}
