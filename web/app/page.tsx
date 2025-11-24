'use client';

import * as React from 'react';
import { Card, CardContent, CircularProgress, Stack, Typography } from '@mui/material';
import Grid from '@mui/material/Grid2';
import AuthGate, { ApiErrorAlert } from '../components/AuthGate';
import SessionsTable from '../components/SessionsTable';
import { useSessions } from '../lib/use-sessions';

function StatCard({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Card>
      <CardContent>
        <Typography variant="h4" sx={{ fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>
          {value}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {label}
        </Typography>
      </CardContent>
    </Card>
  );
}

function Overview() {
  const { sessions, error, refresh } = useSessions();

  if (error) return <ApiErrorAlert error={error} />;
  if (!sessions) return <CircularProgress size={22} />;

  const connected = sessions.filter((s) => s.connected).length;
  const requests = sessions.reduce((sum, s) => sum + s.requests, 0);

  return (
    <Stack spacing={3}>
      <Grid container spacing={1.5}>
        <Grid size={{ xs: 6, sm: 3 }}>
          <StatCard label="connected" value={connected} />
        </Grid>
        <Grid size={{ xs: 6, sm: 3 }}>
          <StatCard label="sessions" value={sessions.length} />
        </Grid>
        <Grid size={{ xs: 6, sm: 3 }}>
          <StatCard label="requests proxied" value={requests} />
        </Grid>
        <Grid size={{ xs: 6, sm: 3 }}>
          <StatCard label="offline" value={sessions.length - connected} />
        </Grid>
      </Grid>

      <div>
        <Typography variant="h2" gutterBottom>
          Live tunnels
        </Typography>
        <SessionsTable sessions={sessions} dense onDeleted={() => void refresh()} />
      </div>
    </Stack>
  );
}

export default function HomePage() {
  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">Overview</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 640 }}>
          Public HTTPS URLs for local HTTP services. A client connects with an API key and gets an address that
          forwards straight to whatever it exposes: a port on that machine, a folder, or a handler inside the program
          itself.
        </Typography>
      </div>
      <AuthGate>
        <Overview />
      </AuthGate>
    </Stack>
  );
}
