'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { Button, Card, CardContent, CircularProgress, Stack, Typography } from '@mui/material';
import AuthGate, { ApiErrorAlert } from '../../../components/AuthGate';
import { RevokeDialog, StateChip } from '../../../components/SessionsTable';
import { api, ApiError } from '../../../lib/api';
import { since, stamp } from '../../../lib/format';
import type { Session } from '../../../lib/types';

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={{ xs: 0, sm: 2 }}>
      <Typography variant="body2" color="text.secondary" sx={{ minWidth: 130 }}>
        {label}
      </Typography>
      <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
        {children}
      </Typography>
    </Stack>
  );
}

function Detail({ id }: { id: string }) {
  const router = useRouter();
  const [session, setSession] = React.useState<Session | null>(null);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [confirming, setConfirming] = React.useState(false);

  React.useEffect(() => {
    let live = true;
    const load = async () => {
      try {
        const s = await api.getSession(id);
        if (live) {
          setSession(s);
          setError(null);
        }
      } catch (e) {
        if (live) setError(e instanceof ApiError ? e : new ApiError(0, String(e)));
      }
    };
    load();
    const t = setInterval(load, 5000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [id]);

  if (error) return <ApiErrorAlert error={error} />;
  if (!session) return <CircularProgress size={22} />;

  return (
    <Stack spacing={3}>
      <Stack direction="row" spacing={2} alignItems="center">
        <Typography variant="h1">{session.subdomain}</Typography>
        <StateChip connected={session.connected} />
      </Stack>

      <Card>
        <CardContent>
          <Stack spacing={1}>
            <Field label="public url">
              <a href={session.url}>{session.url}</a>
            </Field>
            <Field label="session id">
              <code>{session.session}</code>
            </Field>
            <Field label="api key">{session.key_name || '-'}</Field>
            <Field label="connected from">{session.remote_addr || '-'}</Field>
            <Field label="requests">{session.requests}</Field>
            <Field label="created">
              {stamp(session.created_at)} ({since(session.created_at)})
            </Field>
            <Field label="last seen">
              {stamp(session.last_seen)} ({since(session.last_seen)})
            </Field>
          </Stack>
        </CardContent>
      </Card>

      <div>
        <Typography variant="h2" gutterBottom>
          Reconnect
        </Typography>
        <Typography variant="body2" color="text.secondary" gutterBottom>
          Put this in the client section of config.json to reclaim the same URL:
        </Typography>
        <Card>
          <CardContent sx={{ fontFamily: 'ui-monospace, monospace', fontSize: '0.85rem' }}>
            &quot;session_id&quot;: &quot;{session.session}&quot;
          </CardContent>
        </Card>
      </div>

      <div>
        <Button color="error" variant="outlined" size="small" onClick={() => setConfirming(true)}>
          revoke session
        </Button>
      </div>

      <RevokeDialog
        session={session}
        open={confirming}
        onClose={() => setConfirming(false)}
        onDeleted={() => router.push('/sessions')}
      />
    </Stack>
  );
}

export default function SessionClient() {
  // The exported page is served for every /sessions/{id}, so the id comes from
  // the address bar rather than from route params.
  const [id, setId] = React.useState<string | null>(null);
  React.useEffect(() => {
    const parts = window.location.pathname.split('/').filter(Boolean);
    setId(decodeURIComponent(parts[1] ?? ''));
  }, []);

  if (id === null) return null;
  if (!id || id === '__placeholder__') {
    return <Typography variant="body2">No session selected.</Typography>;
  }
  return (
    <AuthGate>
      <Detail id={id} />
    </AuthGate>
  );
}
