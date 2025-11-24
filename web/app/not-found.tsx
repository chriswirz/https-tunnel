import * as React from 'react';
import { Stack, Typography } from '@mui/material';

/**
 * Served for an unknown path on the control plane, and by the proxy for any
 * hostname it does not recognize.
 *
 * On a tunnel hostname none of this application's assets exist: /_next belongs
 * to whatever that client is serving, so every script and stylesheet request
 * there is a 404 and no JavaScript ever runs. The page therefore has to be
 * correct as plain HTML. MUI inlines its critical CSS during the export, which
 * covers the styling, and the one thing that has to vary, the address of the
 * main site, is substituted by the server before the bytes go out.
 */
export default function NotFound() {
  return (
    <Stack spacing={2}>
      <Typography variant="h1">404</Typography>
      <Typography variant="body2" color="text.secondary">
        No page here, and no tunnel is serving this hostname.
      </Typography>
      <Typography variant="body2">
        {/* A plain anchor with a placeholder href: this usually points at
            another hostname, and next/link cannot leave the app. */}
        <a href="__TUNNEL_BASE_URL__">Back to the main site</a>
      </Typography>
    </Stack>
  );
}
