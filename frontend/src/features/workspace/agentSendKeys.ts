import type { AgentTarget } from './agentClient'

// Keep uncertain attempts while this account's view is open. Editing a draft or
// visiting another thread must not turn a retry into a second remote message.
export class AgentSendKeys {
  private keys = new Map<string, string>()
  private createKey: () => string

  constructor(createKey: () => string = () => crypto.randomUUID()) {
    this.createKey = createKey
  }

  prepare(target: AgentTarget, text: string) {
    const signature = JSON.stringify([target.connectionId, target.threadId, text])
    let key = this.keys.get(signature)
    if (!key) { key = this.createKey(); this.keys.set(signature, key) }
    return { signature, key }
  }

  complete(signature: string) { this.keys.delete(signature) }
}
