'use client';

import * as React from 'react';
import { Alert, Card, CardContent, Stack, Typography } from '@mui/material';

/**
 * Served by the Go proxy, with a 502, when a request arrives for a tunnel whose
 * client is not connected. It is a normal page of this app so the look matches,
 * and it reads the hostname client side because one page is served for every
 * subdomain.
 */
export default function OfflinePage() {
  const [host, setHost] = React.useState('');
  React.useEffect(() => setHost(window.location.host), []);

  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">Tunnel offline</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 640 }}>
          This URL belongs to a session whose client is not currently connected. Start the client again and the tunnel
          comes straight back on the same address.
        </Typography>
      </div>
      <Alert severity="warning">{host || 'This hostname'} has no client attached right now.</Alert>
      <Card>
        <CardContent>
          <Typography variant="body2" color="text.secondary" gutterBottom>
            On the machine that serves this tunnel:
          </Typography>
          <Typography component="pre" sx={{ m: 0, fontFamily: 'ui-monospace, monospace', fontSize: '0.85rem' }}>
            https-tunnel client
          </Typography>
        </CardContent>
      </Card>
    </Stack>
  );
}
