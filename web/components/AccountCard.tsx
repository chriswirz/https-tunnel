'use client';

import * as React from 'react';
import { Alert, Box, Button, Card, CardContent, Stack, TextField, Typography } from '@mui/material';
import { useAuth } from '../lib/auth-context';

/** The first boot credentials, which stop working the moment either is changed. */
export const INITIAL_USERNAME = 'admin';
export const INITIAL_PASSWORD = 'admin';
const MIN_PASSWORD = 8;
const MIN_USERNAME = 3;

/**
 * Changes the administrator username, password, or both.
 *
 * There is one administrator account, so a new username renames it rather than
 * adding a second: after "admin" becomes "crwirz", nothing signs in as "admin"
 * again, and the default password belongs to the new name.
 *
 * The same card serves two jobs. On first boot it is the wall the UI puts up
 * until a real password exists, where the password is required; afterwards it is
 * the account page, where either field alone is a valid change.
 */
export default function AccountCard({ firstBoot = false }: { firstBoot?: boolean }) {
  const { status, changeAccount, error } = useAuth();
  const currentName = status?.username || INITIAL_USERNAME;

  const [current, setCurrent] = React.useState(firstBoot ? INITIAL_PASSWORD : '');
  const [username, setUsername] = React.useState(currentName);
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [done, setDone] = React.useState<string | null>(null);

  React.useEffect(() => setUsername(currentName), [currentName]);

  const renaming = username.trim().toLowerCase() !== currentName.toLowerCase();
  const badUsername = username.trim().length > 0 && username.trim().length < MIN_USERNAME;
  const mismatch = confirm.length > 0 && next !== confirm;
  const tooShort = next.length > 0 && next.length < MIN_PASSWORD;
  const passwordOk = next.length === 0 || (next.length >= MIN_PASSWORD && next === confirm && next !== INITIAL_PASSWORD);
  const ready =
    current.length > 0 &&
    !badUsername &&
    passwordOk &&
    (firstBoot ? next.length >= MIN_PASSWORD && next === confirm : renaming || next.length > 0);

  return (
    <Card sx={{ width: '100%', maxWidth: 460 }}>
      <CardContent>
        <Typography variant="h2" gutterBottom>
          {firstBoot ? 'Choose a username and password' : 'Administrator account'}
        </Typography>

        {firstBoot && (
          <Alert severity="warning" sx={{ mb: 2 }}>
            This server is still on the default password. Pick a new one before going any further; it is hashed and
            written to config.json. You can rename the account at the same time.
          </Alert>
        )}
        {!firstBoot && (
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            Renaming the account retires the old name: <strong>{currentName}</strong> will stop working, and there is no
            second account left behind under it. Leave the password blank to keep the current one.
          </Typography>
        )}
        {error && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        {done && (
          <Alert severity="success" sx={{ mb: 2 }}>
            {done}
          </Alert>
        )}

        <Box
          component="form"
          onSubmit={async (e: React.FormEvent) => {
            e.preventDefault();
            setBusy(true);
            setDone(null);
            const wanted = username.trim().toLowerCase();
            try {
              await changeAccount(current, {
                username: renaming ? wanted : undefined,
                password: next || undefined,
              });
              setDone(
                renaming && next
                  ? `Signed in as ${wanted}, with the new password.`
                  : renaming
                    ? `Signed in as ${wanted}. The old name no longer works.`
                    : 'Password changed.',
              );
              setCurrent(next || current);
              setNext('');
              setConfirm('');
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
              label="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              error={badUsername}
              helperText={badUsername ? `At least ${MIN_USERNAME} characters.` : ' '}
            />
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
              label={firstBoot ? 'new password' : 'new password (optional)'}
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
              {busy ? 'saving...' : firstBoot ? 'set username and password' : 'save changes'}
            </Button>
          </Stack>
        </Box>
      </CardContent>
    </Card>
  );
}
