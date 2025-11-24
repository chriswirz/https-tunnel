'use client';

import * as React from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { AppBar, Box, Button, Container, Divider, Stack, Toolbar, Typography } from '@mui/material';
import { useAuth } from '../lib/auth-context';

// Overview and sessions are about this server's own state, so they are hidden
// until someone signs in. Nothing was readable without a session anyway; leaving
// the links up only invited a click that ends at a login form.
const NAV = [
  { href: '/', label: 'overview', signedIn: true },
  { href: '/sessions', label: 'sessions', signedIn: true },
  { href: '/docs', label: 'docs', signedIn: true },
  { href: '/swagger', label: 'api', signedIn: true },
  { href: '/account', label: 'account', signedIn: true },
];

function NavLink({ href, label, active }: { href: string; label: string; active: boolean }) {
  return (
    <Typography
      component={Link}
      href={href}
      variant="body2"
      sx={{
        textDecoration: 'none',
        color: active ? 'primary.main' : 'text.secondary',
        '&:hover': { color: 'primary.main' },
      }}
    >
      {label}
    </Typography>
  );
}

export default function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() || '/';
  const { status, signOut } = useAuth();
  // The Swagger page brings its own wide layout and needs the room.
  const wide = pathname.startsWith('/swagger');
  const signedIn = Boolean(status?.authenticated) && !status?.must_change_password;
  const nav = NAV.filter((n) => !n.signedIn || signedIn);

  return (
    <Box sx={{ minHeight: '100dvh', display: 'flex', flexDirection: 'column' }}>
      <AppBar position="static" color="transparent" elevation={0} sx={{ borderBottom: 1, borderColor: 'divider' }}>
        <Toolbar variant="dense" sx={{ gap: 4 }}>
          <Typography component={Link} href="/" sx={{ fontWeight: 650, textDecoration: 'none', color: 'text.primary' }}>
            https-tunnel
          </Typography>
          <Stack direction="row" spacing={2.2}>
            {nav.map((n) => (
              <NavLink
                key={n.href}
                href={n.href}
                label={n.label}
                active={n.href === '/' ? pathname === '/' : pathname.startsWith(n.href)}
              />
            ))}
          </Stack>
          <Box sx={{ flex: 1 }} />
          {signedIn && (
            <Stack direction="row" spacing={1.5} alignItems="center">
              <Typography variant="caption" color="text.secondary">
                {status?.username}
              </Typography>
              <Button size="small" color="inherit" onClick={() => void signOut()}>
                sign out
              </Button>
            </Stack>
          )}
        </Toolbar>
      </AppBar>

      <Container maxWidth={wide ? 'xl' : 'lg'} sx={{ flex: 1, py: 4 }}>
        {children}
      </Container>

      <Divider />
      <Box component="footer" sx={{ px: 3, py: 2 }}>
        <Stack
          direction="row"
          spacing={1.2}
          alignItems="center"
          justifyContent="center"
          sx={{ color: 'text.secondary', fontSize: '0.75rem' }}
        >
          {signedIn && ( <><Typography component={Link} href="/swagger" variant="inherit" sx={{ color: 'inherit' }}>
            swagger docs
          </Typography>
          <span>&middot;</span>
          <Typography
            component="a"
            href="/openapi.json"
            variant="inherit"
            target="_blank"
            rel="noreferrer"
            sx={{ color: 'inherit' }}
          >
            openapi.json
          </Typography>
          <span>&middot;</span></>)}
          <Typography
            component="a"
            href="https://github.com/chriswirz/https-tunnel"
            variant="inherit"
            target="_blank"
            rel="noreferrer"
            sx={{ color: 'inherit' }}
          >
            source
          </Typography>
        </Stack>
      </Box>
    </Box>
  );
}
