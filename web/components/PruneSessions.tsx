'use client';

import * as React from 'react';
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  MenuItem,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { api, ApiError } from '../lib/api';
import { PRUNE_AGES, type PruneAge, type Session } from '../lib/types';

/** How each window reads in a sentence, rather than as a bare token. */
const LABELS: Record<PruneAge, string> = {
  '5m': '5 minutes',
  '30m': '30 minutes',
  '1h': '1 hour',
  '6h': '6 hours',
  '24h': '24 hours',
  '2d': '2 days',
  '1w': '1 week',
  '1m': '1 month',
};

/** Sessions this would remove, computed here so the confirmation can name them. */
function stale(sessions: Session[], age: PruneAge): Session[] {
  const windows: Record<PruneAge, number> = {
    '5m': 5 * 60e3,
    '30m': 30 * 60e3,
    '1h': 3600e3,
    '6h': 6 * 3600e3,
    '24h': 24 * 3600e3,
    '2d': 48 * 3600e3,
    '1w': 7 * 24 * 3600e3,
    '1m': 30 * 24 * 3600e3,
  };
  const cutoff = Date.now() - windows[age];
  // Connected clients are never touched, whatever their age; the server applies
  // the same rule, this is only so the count shown matches what will happen.
  return sessions.filter((s) => !s.connected && new Date(s.last_seen).getTime() < cutoff);
}

export default function PruneSessions({ sessions, onPruned }: { sessions: Session[]; onPruned: () => void }) {
  const [age, setAge] = React.useState<PruneAge>('24h');
  const [confirming, setConfirming] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [done, setDone] = React.useState<string | null>(null);

  const doomed = stale(sessions, age);

  return (
    <Stack spacing={1.5}>
      <Stack direction="row" spacing={1.5} alignItems="center" flexWrap="wrap">
        <TextField
          select
          size="small"
          label="idle for at least"
          value={age}
          onChange={(e) => setAge(e.target.value as PruneAge)}
          sx={{ minWidth: 190 }}
        >
          {PRUNE_AGES.map((a) => (
            <MenuItem key={a} value={a}>
              {LABELS[a]}
            </MenuItem>
          ))}
        </TextField>
        <Button
          variant="outlined"
          color="error"
          disabled={doomed.length === 0}
          onClick={() => {
            setError(null);
            setDone(null);
            setConfirming(true);
          }}
        >
          {doomed.length === 0 ? 'nothing to clean up' : `clean up ${doomed.length}`}
        </Button>
        <Typography variant="caption" color="text.secondary">
          Connected tunnels are never removed.
        </Typography>
      </Stack>

      {error && <Alert severity="error">{error}</Alert>}
      {done && <Alert severity="success">{done}</Alert>}

      <Dialog open={confirming} onClose={busy ? undefined : () => setConfirming(false)}>
        <DialogTitle>Delete {doomed.length} idle sessions?</DialogTitle>
        <DialogContent>
          <DialogContentText component="div">
            Every disconnected session with no activity for {LABELS[age]} is removed and its subdomain freed:
            <Typography component="div" variant="caption" sx={{ mt: 1, fontFamily: 'ui-monospace, monospace' }}>
              {doomed
                .slice(0, 12)
                .map((s) => s.subdomain)
                .join(', ')}
              {doomed.length > 12 ? `, and ${doomed.length - 12} more` : ''}
            </Typography>
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)} disabled={busy}>
            cancel
          </Button>
          <Button
            color="error"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                const result = await api.pruneSessions(age);
                setDone(`Deleted ${result.deleted} session${result.deleted === 1 ? '' : 's'}.`);
                setConfirming(false);
                onPruned();
              } catch (e) {
                setError(e instanceof ApiError ? e.message : String(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? 'deleting...' : 'delete'}
          </Button>
        </DialogActions>
      </Dialog>
    </Stack>
  );
}
