import { createClient } from '@supabase/supabase-js'
import { waitForEvenAppBridge } from '@evenrealities/even_hub_sdk'

import { env } from '../config/env'
import { createSessionStorage } from './session-storage'

export const sessionStorage = createSessionStorage({
  async resolveBridge() {
    // Beta packages must wait for native startup, even if Flutter injects its
    // handler after this module loads. Ordinary browser builds retain web auth.
    const nativeHost = import.meta.env.MODE === 'beta' || 'flutter_inappwebview' in window
    return nativeHost ? waitForEvenAppBridge() : null
  },
  browserStorage: () => window.localStorage,
})

export const supabase = createClient(
  env.supabaseUrl,
  env.supabasePublishableKey,
  {
    auth: {
      storage: sessionStorage,
      persistSession: true,
      autoRefreshToken: true,
    },
  },
)
