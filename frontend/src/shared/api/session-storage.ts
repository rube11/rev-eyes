type BrowserStorage = {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

export type EvenSessionBridge = {
  getUserInfo(): Promise<{ uid: number }>
  getLocalStorage(key: string): Promise<string>
  setLocalStorage(key: string, value: string): Promise<boolean>
}

type SessionStorage = {
  getItem(key: string): Promise<string | null> | string | null
  setItem(key: string, value: string): Promise<void> | void
  removeItem(key: string): Promise<void> | void
}

type Options = {
  resolveBridge(): Promise<EvenSessionBridge | null>
  browserStorage(): BrowserStorage
  timeoutMs?: number
}

const storageError = 'Saved sign-in storage is unavailable. Reopen the app to retry.'

// One authoritative store per launch. Do not mirror refresh tokens: an older
// browser copy could resurrect a signed-out session or replay a rotated token.
export function createSessionStorage(options: Options) {
  let selected: Promise<SessionStorage> | undefined
  let failure = ''
  const listeners = new Set<() => void>()

  async function selectStorage(): Promise<SessionStorage> {
    const bridge = await options.resolveBridge()
    if (!bridge) return options.browserStorage()

    const { uid } = await bridge.getUserInfo()
    if (!Number.isSafeInteger(uid) || uid <= 0) throw new Error(storageError)
    // Partition sessions on relaunch after an Even-account switch. The UID is
    // only a storage namespace; Supabase tokens still authorize every request.
    const nativeKey = (key: string) => `com.reveyes.app.auth.${uid}.${key}`
    const write = async (key: string, value: string) => {
      if (await bridge.setLocalStorage(nativeKey(key), value) !== true) {
        throw new Error(storageError)
      }
    }
    return {
      async getItem(key) {
        const value = await bridge.getLocalStorage(nativeKey(key))
        if (typeof value !== 'string') throw new Error(storageError)
        return value || null
      },
      setItem: write,
      // The Even SDK has no remove method; an empty string is its missing value.
      removeItem: (key) => write(key, ''),
    }
  }

  async function run<T>(operation: (storage: SessionStorage) => T | Promise<T>): Promise<T> {
    if (failure) throw new Error(failure)
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      return await Promise.race([
        (async () => {
          const storage = await (selected ??= selectStorage())
          if (failure) throw new Error(failure)
          return operation(storage)
        })(),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error(storageError)), options.timeoutMs ?? 3_000)
        }),
      ])
    } catch {
      // Fail closed for this launch. Native calls cannot be cancelled, so after
      // a timeout do not issue later writes or silently fall back to stale data.
      failure = storageError
      for (const listener of listeners) listener()
      throw new Error(storageError)
    } finally {
      clearTimeout(timer)
    }
  }

  return {
    getItem: (key: string) => run((storage) => storage.getItem(key)),
    setItem: (key: string, value: string) => run((storage) => storage.setItem(key, value)),
    removeItem: (key: string) => run((storage) => storage.removeItem(key)),
    getError: () => failure,
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
  }
}
