import * as React from 'react';
import type { Metadata } from 'next';
import { AppRouterCacheProvider } from '@mui/material-nextjs/v14-appRouter';
import { ThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import theme from './theme';
import AppShell from '../components/AppShell';
import { AuthProvider } from '../lib/auth-context';

export const metadata: Metadata = {
  title: 'https-tunnel',
  description: 'Public HTTPS URLs for local HTTP services.',
  // The server rewrites this when it serves a page on a tunnel hostname, which
  // is how a 404 there can link back to the control plane. See lib/site.ts.
  other: { 'tunnel-base-url': '__TUNNEL_BASE_URL__' },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <AppRouterCacheProvider options={{ enableCssLayer: true }}>
          <ThemeProvider theme={theme}>
            <CssBaseline />
            <AuthProvider>
              <AppShell>{children}</AppShell>
            </AuthProvider>
          </ThemeProvider>
        </AppRouterCacheProvider>
      </body>
    </html>
  );
}
