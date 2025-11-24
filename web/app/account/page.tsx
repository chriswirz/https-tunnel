'use client';

import * as React from 'react';
import { Stack, Typography } from '@mui/material';
import AuthGate from '../../components/AuthGate';
import AccountCard from '../../components/AccountCard';

export default function AccountPage() {
  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">Account</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 640 }}>
          The single administrator account for this server. Changes are hashed and written to config.json, and every
          other signed in browser is dropped.
        </Typography>
      </div>
      <AuthGate>
        <AccountCard />
      </AuthGate>
    </Stack>
  );
}
