/**
 * The logged-out screen: sign in, or create an account, in one place.
 *
 * Client-side constraints here are UX only. The auth service validates that
 * email, username, and password are non-empty and nothing more (SPEC.md
 * 2.12), so this form must not imply guarantees the server does not make --
 * and the server's own {code, message} is what gets shown on rejection.
 */
import { useId, useState, type FormEvent } from 'react'

import { ApiError } from '../api/client'
import { useAuth } from './context'

type Mode = 'login' | 'register'

export default function LoginPage() {
  const { login, register } = useAuth()
  const [mode, setMode] = useState<Mode>('login')
  const [email, setEmail] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const errorId = useId()
  const isRegister = mode === 'register'

  function switchMode() {
    setMode(isRegister ? 'login' : 'register')
    setError(null)
    setPassword('')
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      if (isRegister) {
        await register({ email, username, password })
      } else {
        await login({ email, password })
      }
    } catch (caught) {
      // Show what the backend said, not a message invented here.
      setError(
        caught instanceof ApiError
          ? caught.message
          : 'Something went wrong. Please try again.',
      )
      setSubmitting(false)
    }
  }

  return (
    <main className="min-h-dvh grid place-items-center px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-2.5">
          <BrandMark />
          <span className="text-lg font-semibold tracking-tight text-ink">
            QuantSim
          </span>
        </div>

        <h1 className="mt-7 text-2xl font-semibold tracking-tight text-ink">
          {isRegister ? 'Create your account' : 'Sign in'}
        </h1>
        <p className="mt-1.5 text-sm text-ink-muted">
          {isRegister
            ? 'Paper trading with live market data. Starting balance $100,000.'
            : 'Welcome back. Your simulated portfolio is waiting.'}
        </p>

        <form onSubmit={handleSubmit} className="mt-7 space-y-4" noValidate>
          <Field
            label="Email"
            type="email"
            value={email}
            onChange={setEmail}
            autoComplete="email"
            describedBy={error ? errorId : undefined}
            autoFocus
          />

          {isRegister && (
            <Field
              label="Username"
              type="text"
              value={username}
              onChange={setUsername}
              autoComplete="username"
              describedBy={error ? errorId : undefined}
            />
          )}

          <Field
            label="Password"
            type="password"
            value={password}
            onChange={setPassword}
            autoComplete={isRegister ? 'new-password' : 'current-password'}
            describedBy={error ? errorId : undefined}
            hint={isRegister ? 'At least 8 characters.' : undefined}
          />

          {error && (
            <p
              id={errorId}
              role="alert"
              className="flex items-start gap-2 rounded-md border border-danger/40 bg-danger-bg px-3 py-2 text-sm text-ink"
            >
              {/* An icon as well as colour: colour alone is not an
                  accessible signal. */}
              <span aria-hidden="true" className="mt-px font-bold text-danger">
                !
              </span>
              <span>{error}</span>
            </p>
          )}

          <button
            type="submit"
            disabled={submitting}
            aria-busy={submitting}
            className="w-full rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-canvas transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-60"
          >
            {submitting
              ? isRegister
                ? 'Creating account…'
                : 'Signing in…'
              : isRegister
                ? 'Create account'
                : 'Sign in'}
          </button>
        </form>

        <p className="mt-6 text-sm text-ink-muted">
          {isRegister ? 'Already have an account?' : 'New to QuantSim?'}{' '}
          <button
            type="button"
            onClick={switchMode}
            className="rounded-sm font-medium text-accent hover:text-accent-hover hover:underline"
          >
            {isRegister ? 'Sign in' : 'Create one'}
          </button>
        </p>
      </div>
    </main>
  )
}

interface FieldProps {
  label: string
  type: 'text' | 'email' | 'password'
  value: string
  onChange: (value: string) => void
  autoComplete: string
  describedBy?: string
  hint?: string
  autoFocus?: boolean
}

function Field({
  label,
  type,
  value,
  onChange,
  autoComplete,
  describedBy,
  hint,
  autoFocus,
}: FieldProps) {
  const id = useId()
  const hintId = `${id}-hint`

  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium text-ink">
        {label}
      </label>
      <input
        id={id}
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete={autoComplete}
        // eslint-disable-next-line jsx-a11y/no-autofocus -- single-purpose
        // screen whose only job is this form; focusing the first field is
        // what a keyboard user wants here.
        autoFocus={autoFocus}
        required
        aria-describedby={
          [hint ? hintId : null, describedBy].filter(Boolean).join(' ') ||
          undefined
        }
        className="mt-1.5 w-full rounded-md border border-line-strong bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink-subtle focus:border-accent focus:outline-none"
      />
      {hint && (
        <p id={hintId} className="mt-1.5 text-xs text-ink-subtle">
          {hint}
        </p>
      )}
    </div>
  )
}

function BrandMark() {
  return (
    <svg
      viewBox="0 0 32 32"
      className="h-7 w-7"
      role="img"
      aria-label="QuantSim"
    >
      <rect width="32" height="32" rx="6" className="fill-elevated" />
      <path
        d="M5 22.5 L12 15 L17 19 L27 8"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.75"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="text-up"
      />
      <circle cx="27" cy="8" r="2.75" className="fill-up" />
    </svg>
  )
}
