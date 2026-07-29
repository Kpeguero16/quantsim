/**
 * The auth context object and its consumer hook. Deliberately separate from
 * AuthProvider.tsx: a module that exports both a component and a plain
 * function breaks React Fast Refresh, so components live on their own.
 */
import { createContext, useContext } from 'react'

import type { LoginRequest, MeResponse, RegisterRequest } from '../api/types'

export interface AuthContextValue {
  user: MeResponse | null
  login: (request: LoginRequest) => Promise<void>
  register: (request: RegisterRequest) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext)
  if (!context) throw new Error('useAuth must be used inside <AuthProvider>')
  return context
}
