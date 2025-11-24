'use client';

import type { AuthStatus, PruneAge, PruneResult, Session, SessionList } from './types';

/**
 * Thrown for any non-2xx response, carrying the status so callers can tell an
 * expired sign in (401) apart from a real failure.
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// The browser is authenticated by the admin session cookie the server sets at
// sign in. It is HttpOnly, so there is nothing for this code to hold on to.
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set('Content-Type', 'application/json');
  const resp = await fetch(path, { ...init, headers, cache: 'no-store', credentials: 'same-origin' });
  if (!resp.ok) {
    let message = resp.statusText;
    try {
      const body = (await resp.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // A non-JSON error body is not worth reporting verbatim.
    }
    throw new ApiError(resp.status, message);
  }
  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}

export const auth = {
  status: () => request<AuthStatus>('/api/v1/auth/session'),
  login: (username: string, password: string) =>
    request<AuthStatus>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>('/api/v1/auth/logout', { method: 'POST' }),
  /**
   * Changes the username, the password, or both. There is one administrator
   * account, so a new username renames it: the old name stops working.
   */
  changeAccount: (currentPassword: string, next: { username?: string; password?: string }) =>
    request<AuthStatus>('/api/v1/auth/account', {
      method: 'POST',
      body: JSON.stringify({
        current_password: currentPassword,
        new_username: next.username || undefined,
        new_password: next.password || undefined,
      }),
    }),
};

export const api = {
  listSessions: () => request<SessionList>('/api/v1/sessions'),
  getSession: (id: string) => request<Session>(`/api/v1/sessions/${encodeURIComponent(id)}`),
  deleteSession: (id: string) =>
    request<void>(`/api/v1/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  /** Deletes every disconnected session idle longer than the given window. */
  pruneSessions: (idleFor: PruneAge) =>
    request<PruneResult>(`/api/v1/sessions?idle_for=${encodeURIComponent(idleFor)}`, { method: 'DELETE' }),
};
