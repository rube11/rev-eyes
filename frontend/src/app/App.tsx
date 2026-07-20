import { useEffect, useState } from "react"
import type { FormEvent } from "react"
import type { Session } from "@supabase/supabase-js"

import {
  initializeEvenExperience,
  showEvenMessage,
} from "../even/runtime"
import { supabase } from "../shared/api/supabase"

function App() {
  const [session, setSession] = useState<Session | null>()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [status, setStatus] = useState("Connecting")
  const [response, setResponse] = useState("")
  const accessToken = session?.access_token

  useEffect(() => {
    const { data } = supabase.auth.onAuthStateChange((_event, nextSession) => {
      setSession(nextSession)
    })
    return () => data.subscription.unsubscribe()
  }, [])

  useEffect(() => {
    if (session === null) {
      void showEvenMessage("Open phone to sign in").catch(() => undefined)
    }
  }, [session])

  useEffect(() => {
    if (!accessToken) {
      return
    }

    let disposed = false
    let stop: (() => void) | undefined
    initializeEvenExperience(
      accessToken,
      (text) => {
        if (!disposed) {
          setResponse(text)
        }
      },
      (nextStatus) => {
        if (!disposed) {
          setStatus(nextStatus)
        }
      },
    )
      .then((cleanup) => {
        if (disposed) {
          cleanup()
          return
        }
        stop = cleanup
      })
      .catch((reason: unknown) => {
        if (!disposed) {
          setStatus(reason instanceof Error ? reason.message : "Connection failed")
        }
      })

    return () => {
      disposed = true
      stop?.()
    }
  }, [accessToken])

  const signIn = async (event: FormEvent) => {
    event.preventDefault()
    setError("")
    const { error: signInError } = await supabase.auth.signInWithPassword({
      email,
      password,
    })
    if (signInError) {
      setError(signInError.message)
      return
    }
    setPassword("")
    setStatus("Connecting")
  }

  if (session === undefined) {
    return <main className="app">Loading…</main>
  }

  if (!session) {
    return (
      <main className="app">
        <form className="app-shell" onSubmit={signIn}>
          <p className="eyebrow">rev-eyes</p>
          <h1>Sign in</h1>
          <label>
            Email
            <input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              required
            />
          </label>
          <label>
            Password
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              required
            />
          </label>
          {error && <p role="alert">{error}</p>}
          <button type="submit">Sign in</button>
        </form>
      </main>
    )
  }

  return (
    <main className="app">
      <section className="app-shell">
        <p className="eyebrow">rev-eyes</p>
        <h1>{status}</h1>
        <p>{response || "Assistant responses will appear here."}</p>
        <button type="button" onClick={() => void supabase.auth.signOut()}>
          Sign out
        </button>
      </section>
    </main>
  )
}

export default App
