export type DiagnosticEntry = {
  at: number
  event: string
  data: Record<string, unknown>
}
type Storage = { getItem(key: string): string | null; setItem(key: string, value: string): void }
const KEY = "rev-eyes.moonshine-diagnostics.v1"
export const MAX_ENTRIES = 500

// No audio, tokens, or account data. Bounded so long beta sessions cannot grow storage forever.
export class DiagnosticLog {
  entries: DiagnosticEntry[] = []
  persistent = true
  constructor(privateStorage: Storage | undefined) {
    this.storage = privateStorage
    try {
      const saved: unknown = JSON.parse(privateStorage?.getItem(KEY) ?? "[]")
      if (Array.isArray(saved)) {
        this.entries = saved.filter((item): item is DiagnosticEntry =>
          item && typeof item.at === "number" && typeof item.event === "string" &&
          item.data && typeof item.data === "object",
        ).slice(-MAX_ENTRIES)
      }
      if (!privateStorage) this.persistent = false
    } catch { this.persistent = false }
  }
  private readonly storage: Storage | undefined
  add(event: string, data: Record<string, unknown> = {}, at = Date.now()) {
    this.entries.push({ at, event, data })
    this.entries = this.entries.slice(-MAX_ENTRIES)
    this.save()
  }
  clear() { this.entries = []; this.save() }
  private save() {
    try {
      if (!this.storage) throw new Error("Storage unavailable")
      this.storage.setItem(KEY, JSON.stringify(this.entries))
      this.persistent = true
    } catch { this.persistent = false }
  }
}
