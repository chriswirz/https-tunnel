'use client';

import * as React from 'react';
import { auth, ApiError } from './api';
import type { AuthStatus } from './types';

interface AuthState {
  status: AuthStatus | null;
  loading: boolean;
  error: string | null;
  signIn: (username: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  changePassword: (current: string, next: string) => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = React.createContext<AuthState | null>(null);

/**
 * Holds the admin sign in state for the whole app.
 * The server owns the session; this only mirrors what /api/v1/auth/session says,
 * so a cookie that expires or is revoked elsewhere shows up on the next call.
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = React.useState<AuthStatus | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  const refresh = React.useCallback(async () => {
    try {
      setStatus(await auth.status());
    } catch (e) {
      setStatus({ authenticated: false, must_change_password: false, password_unset: false });
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  const value: AuthState = {
    status,
    loading,
    error,
    refresh,
    signIn: async (username, password) => {
      setError(null);
      try {
        setStatus(await auth.login(username, password));
      } catch (e) {
        const message = e instanceof ApiError ? e.message : String(e);
        setError(message);
        throw e;
      }
    },
    signOut: async () => {
      await auth.logout();
      await refresh();
    },
    changePassword: async (current, next) => {
      setError(null);
      try {
        setStatus(await auth.changePassword(current, next));
      } catch (e) {
        const message = e instanceof ApiError ? e.message : String(e);
        setError(message);
        throw e;
      }
    },
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = React.useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}
