'use client';

import * as React from 'react';
import Link from 'next/link';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from '@mui/material';
import type { Session } from '../lib/types';
import { since } from '../lib/format';
import { api, ApiError } from '../lib/api';

export function StateChip({ connected }: { connected: boolean }) {
  return (
    <Chip
      size="small"
      variant="outlined"
      color={connected ? 'success' : 'default'}
      label={connected ? 'up' : 'down'}
    />
  );
}

/**
 * Confirms and performs a revocation.
 * Deleting a session frees its subdomain and drops the tunnel, and a running
 * client will reconnect on a new URL, so it is worth asking first.
 */
export function RevokeDialog({
  session,
  open,
  onClose,
  onDeleted,
}: {
  session: Session | null;
  open: boolean;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (open) setError(null);
  }, [open]);

  if (!session) return null;

  return (
    <Dialog open={open} onClose={busy ? undefined : onClose}>
      <DialogTitle>Revoke {session.subdomain}?</DialogTitle>
      <DialogContent>
        <DialogContentText component="div">
          The subdomain is freed and {session.connected ? 'the connected client is disconnected' : 'the session is removed'}.
          A client that is still running will reconnect and be issued a new session, on a new URL.
        </DialogContentText>
        {error && (
          <Alert severity="error" sx={{ mt: 2 }}>
            {error}
          </Alert>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={busy}>
          cancel
        </Button>
        <Button
          color="error"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await api.deleteSession(session.session);
              onDeleted?.();
              onClose();
            } catch (e) {
              setError(e instanceof ApiError ? e.message : String(e));
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy ? 'revoking...' : 'revoke'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

export default function SessionsTable({
  sessions,
  dense,
  onDeleted,
}: {
  sessions: Session[];
  dense?: boolean;
  /** When given, each row gets a revoke button and this is called after one succeeds. */
  onDeleted?: () => void;
}) {
  const [revoking, setRevoking] = React.useState<Session | null>(null);

  if (sessions.length === 0) {
    return (
      <Typography variant="body2" color="text.secondary">
        No sessions yet. Start a client with <code>https-tunnel client</code>.
      </Typography>
    );
  }

  return (
    <Box sx={{ overflowX: 'auto' }}>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>state</TableCell>
            <TableCell>subdomain</TableCell>
            {!dense && <TableCell>session</TableCell>}
            <TableCell>key</TableCell>
            <TableCell align="right">requests</TableCell>
            {!dense && <TableCell>created</TableCell>}
            <TableCell>last seen</TableCell>
            {onDeleted && <TableCell align="right" />}
          </TableRow>
        </TableHead>
        <TableBody>
          {sessions.map((s) => (
            <TableRow key={s.session} hover>
              <TableCell>
                <StateChip connected={s.connected} />
              </TableCell>
              <TableCell>
                <Link href={`/sessions/${s.session}`}>{s.subdomain}</Link>
              </TableCell>
              {!dense && (
                <TableCell>
                  <Typography variant="caption" color="text.secondary" fontFamily="ui-monospace, monospace">
                    {s.session}
                  </Typography>
                </TableCell>
              )}
              <TableCell>{s.key_name || '-'}</TableCell>
              <TableCell align="right">{s.requests}</TableCell>
              {!dense && <TableCell>{since(s.created_at)}</TableCell>}
              <TableCell>{since(s.last_seen)}</TableCell>
              {onDeleted && (
                <TableCell align="right" padding="checkbox">
                  <Tooltip title="revoke this session">
                    <IconButton size="small" aria-label={`revoke ${s.subdomain}`} onClick={() => setRevoking(s)}>
                      <DeleteOutlineIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <RevokeDialog
        session={revoking}
        open={revoking !== null}
        onClose={() => setRevoking(null)}
        onDeleted={onDeleted}
      />
    </Box>
  );
}
