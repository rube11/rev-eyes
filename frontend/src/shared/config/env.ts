function required(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value.replace(/\/$/, '')
}

export const env = {
  serverListeningEnabled: import.meta.env.VITE_SERVER_LISTENING_ENABLED?.trim().toLowerCase() === 'true',
  apiBaseUrl: required('VITE_API_BASE_URL', import.meta.env.VITE_API_BASE_URL),
  supabaseUrl: required('VITE_SUPABASE_URL', import.meta.env.VITE_SUPABASE_URL),
  supabasePublishableKey: required(
    'VITE_SUPABASE_PUBLISHABLE_KEY',
    import.meta.env.VITE_SUPABASE_PUBLISHABLE_KEY,
  ),
}
