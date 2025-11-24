'use client';

import * as React from 'react';
import { api, ApiError } from './api';
import type { Session } from './types';

/** Polls the session list so the table reflects clients coming and going. */
export function useSessions(intervalMs = 5000) {
  const [sessions, setSessions] = React.useState<Session[] | null>(null);
  const [error, setError] = React.useState<ApiError | null>(null);
  const live = React.useRef(true);

  const load = React.useCallback(async () => {
    try {
      const data = await api.listSessions();
      if (!live.current) return;
      setSessions(data.sessions ?? []);
      setError(null);
    } catch (e) {
      if (!live.current) return;
      setError(e instanceof ApiError ? e : new ApiError(0, String(e)));
    }
  }, []);

  React.useEffect(() => {
    live.current = true;
    void load();
    const t = setInterval(() => void load(), intervalMs);
    return () => {
      live.current = false;
      clearInterval(t);
    };
  }, [intervalMs, load]);

  // refresh lets an action that changed something show the result at once, rather than waiting out the poll interval.
  return { sessions, error, refresh: load };
}
