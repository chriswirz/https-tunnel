'use client';

import * as React from 'react';
import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  CircularProgress,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { useAuth } from '../lib/auth-context';

/** The first boot credentials, shown on the login form while no password is set. */
const INITIAL_USERNAME = 'admin';
const INITIAL_PASSWORD = 'admin';
const MIN_PASSWORD = 8;

function LoginCard() {
  const { signIn, error, status } = useAuth();
  const [username, setUsername] = React.useState(INITIAL_USERNAME);
  const [password, setPassword] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  return (
    <Card sx={{ maxWidth: 420 }}>
      <CardContent>
        <Typography variant="h2" gutterBottom>
          Sign in
        </Typography>
        {status?.password_unset && (
          <Alert severity="info" sx={{ mb: 2 }}>
            No password has been set yet. Sign in with <strong>{INITIAL_USERNAME}</strong> /{' '}
            <strong>{INITIAL_PASSWORD}</strong> and you will be asked to choose one.
          </Alert>
        )}
        {error && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        <Box
          component="form"
          onSubmit={async (e: React.FormEvent) => {
            e.preventDefault();
            setBusy(true);
            try {
              await signIn(username, password);
            } catch {
              // The message is already in the context.
            } finally {
              setBusy(false);
              setPassword('');
            }
          }}
        >
          <Stack spacing={2}>
            <TextField
              size="small"
              label="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
            />
            <TextField
              size="small"
              type="password"
              label="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
            <Button type="submit" variant="contained" disabled={busy || !username || !password}>
              {busy ? 'signing in...' : 'sign in'}
            </Button>
          </Stack>
        </Box>
      </CardContent>
    </Card>
  );
}

function ChangePasswordCard() {
  const { changePassword, error } = useAuth();
  const [current, setCurrent] = React.useState(INITIAL_PASSWORD);
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  const mismatch = confirm.length > 0 && next !== confirm;
  const tooShort = next.length > 0 && next.length < MIN_PASSWORD;
  const ready = next.length >= MIN_PASSWORD && next === confirm && next !== INITIAL_PASSWORD;

  return (
    <Card sx={{ maxWidth: 460 }}>
      <CardContent>
        <Typography variant="h2" gutterBottom>
          Choose a password
        </Typography>
        <Alert severity="warning" sx={{ mb: 2 }}>
          This server is still on the default password. Pick a new one before going any further; it is hashed and
          written to config.json.
        </Alert>
        {error && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        <Box
          component="form"
          onSubmit={async (e: React.FormEvent) => {
            e.preventDefault();
            setBusy(true);
            try {
              await changePassword(current, next);
            } catch {
              // The message is already in the context.
            } finally {
              setBusy(false);
            }
          }}
        >
          <Stack spacing={2}>
            <TextField
              size="small"
              type="password"
              label="current password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              autoComplete="current-password"
            />
            <TextField
              size="small"
              type="password"
              label="new password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              autoComplete="new-password"
              error={tooShort}
              helperText={tooShort ? `At least ${MIN_PASSWORD} characters.` : ' '}
            />
            <TextField
              size="small"
              type="password"
              label="confirm new password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              error={mismatch}
              helperText={mismatch ? 'The two entries do not match.' : ' '}
            />
            <Button type="submit" variant="contained" disabled={busy || !ready}>
              {busy ? 'saving...' : 'set password'}
            </Button>
          </Stack>
        </Box>
      </CardContent>
    </Card>
  );
}

/**
 * Wraps anything that needs an administrator: not signed in shows the login
 * form, and a first boot session is held at the password change until it picks
 * one, which is the same rule the server enforces on the API.
 */
export default function AuthGate({ children }: { children: React.ReactNode }) {
  const { status, loading } = useAuth();

  if (loading) return <CircularProgress size={22} />;
  if (!status?.authenticated) return <LoginCard />;
  if (status.must_change_password) return <ChangePasswordCard />;
  return <>{children}</>;
}

/** Shared rendering for a failed API call, so every page reports errors the same way. */
export function ApiErrorAlert({ error }: { error: { status?: number; message: string } }) {
  const { refresh } = useAuth();
  const expired = error.status === 401;
  return (
    <Alert
      severity={expired ? 'warning' : 'error'}
      action={
        expired ? (
          <Button color="inherit" size="small" onClick={() => void refresh()}>
            sign in again
          </Button>
        ) : undefined
      }
    >
      {expired ? 'That session is no longer valid.' : error.message}
    </Alert>
  );
}
