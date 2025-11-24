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
import AccountCard, { INITIAL_PASSWORD, INITIAL_USERNAME } from './AccountCard';

function LoginCard() {
  const { signIn, error, status } = useAuth();
  const [username, setUsername] = React.useState(INITIAL_USERNAME);
  const [password, setPassword] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  return (
    <Card sx={{ width: '100%', maxWidth: 420 }}>
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

/** Centers the sign in and password cards, which are the whole page when they show. */
function Centered({ children }: { children: React.ReactNode }) {
  return (
    <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'flex-start', pt: { xs: 2, sm: 6 } }}>
      {children}
    </Box>
  );
}

/**
 * Wraps anything that needs an administrator: not signed in shows the login
 * form, and a first boot session is held at the password change until it picks
 * one, which is the same rule the server enforces on the API.
 */
export default function AuthGate({ children }: { children: React.ReactNode }) {
  const { status, loading } = useAuth();

  if (loading) {
    return (
      <Centered>
        <CircularProgress size={22} />
      </Centered>
    );
  }
  if (!status?.authenticated) {
    return (
      <Centered>
        <LoginCard />
      </Centered>
    );
  }
  if (status.must_change_password) {
    return (
      <Centered>
        <AccountCard firstBoot />
      </Centered>
    );
  }
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
