'use client';

import * as React from 'react';
import Link from 'next/link';
import { Stack, Typography } from '@mui/material';

export default function NotFound() {
  return (
    <Stack spacing={2}>
      <Typography variant="h1">404</Typography>
      <Typography variant="body2" color="text.secondary">
        No page here, and no tunnel is serving this hostname.
      </Typography>
      <Typography variant="body2">
        <Link href="/">Back to the overview</Link>
      </Typography>
    </Stack>
  );
}
