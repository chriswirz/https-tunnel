import * as React from 'react';
import { Alert, Card, CardContent, Stack, Typography } from '@mui/material';

/**
 * Served by the Go proxy, with a 502, when a signed in administrator opens a
 * tunnel whose client is not connected.
 *
 * Like the 404 page, this is served on a tunnel hostname where none of this
 * application's assets resolve, so it has to work as plain HTML with no
 * JavaScript. The link back to the main site is substituted by the server.
 */
export default function OfflinePage() {
  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">Tunnel offline</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 640 }}>
          This URL belongs to a session whose client is not currently connected. Start the client again and the tunnel
          comes straight back on the same address.
        </Typography>
      </div>
      <Alert severity="warning" sx={{ maxWidth: 640 }}>
        This hostname has no client attached right now.
      </Alert>
      <Card sx={{ maxWidth: 640 }}>
        <CardContent>
          <Typography variant="body2" color="text.secondary" gutterBottom>
            On the machine that serves this tunnel:
          </Typography>
          <Typography component="pre" sx={{ m: 0, fontFamily: 'ui-monospace, monospace', fontSize: '0.85rem' }}>
            https-tunnel client
          </Typography>
        </CardContent>
      </Card>
      <Typography variant="body2">
        {/* A plain anchor, not next/link: the control plane is another hostname. */}
        <a href="__TUNNEL_BASE_URL__">Back to the main site</a>
      </Typography>
    </Stack>
  );
}
