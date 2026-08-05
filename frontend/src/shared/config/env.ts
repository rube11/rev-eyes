function required(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value.replace(/\/$/, '')
}

function enabled(value: string | undefined): boolean {
  return value?.trim().toLowerCase() === 'true'
}

export const env = {
  apiBaseUrl: required('VITE_API_BASE_URL', import.meta.env.VITE_API_BASE_URL),
  supabaseUrl: required('VITE_SUPABASE_URL', import.meta.env.VITE_SUPABASE_URL),
  supabasePublishableKey: required(
    'VITE_SUPABASE_PUBLISHABLE_KEY',
    import.meta.env.VITE_SUPABASE_PUBLISHABLE_KEY,
  ),
  moonshineShadowEnabled: enabled(
    import.meta.env.VITE_MOONSHINE_SHADOW_ENABLED,
  ),
  moonshineDebugTranscripts: enabled(
    import.meta.env.VITE_MOONSHINE_DEBUG_TRANSCRIPTS,
  ),
  candidateAudioEnabled: enabled(
    import.meta.env.VITE_CANDIDATE_AUDIO_ENABLED,
  ),
  continuousListeningEnabled: enabled(
    import.meta.env.VITE_CONTINUOUS_LISTENING_ENABLED,
  ),
}
