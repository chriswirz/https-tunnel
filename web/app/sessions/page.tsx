'use client';

import * as React from 'react';
import { CircularProgress, Stack, Typography } from '@mui/material';
import AuthGate, { ApiErrorAlert } from '../../components/AuthGate';
import SessionsTable from '../../components/SessionsTable';
import { useSessions } from '../../lib/use-sessions';

function List() {
  const { sessions, error, refresh } = useSessions();
  if (error) return <ApiErrorAlert error={error} />;
  if (!sessions) return <CircularProgress size={22} />;
  return <SessionsTable sessions={sessions} onDeleted={() => void refresh()} />;
}

export default function SessionsPage() {
  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">Sessions</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 640 }}>
          Every session this server has issued. Identities survive restarts, so a client that keeps its session id keeps
          its URL. Revoking one frees its subdomain and drops the tunnel.
        </Typography>
      </div>
      <AuthGate>
        <List />
      </AuthGate>
    </Stack>
  );
}
