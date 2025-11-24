'use client';

import { createTheme } from '@mui/material/styles';

// One theme that follows the viewer's colour scheme, so the UI matches the
// terminal it is usually opened from.
const theme = createTheme({
  colorSchemes: { light: true, dark: true },
  cssVariables: { colorSchemeSelector: 'media' },
  shape: { borderRadius: 10 },
  typography: {
    fontFamily: 'ui-sans-serif, -apple-system, "Segoe UI", Roboto, sans-serif',
    h1: { fontSize: '1.6rem', fontWeight: 650, letterSpacing: '-0.02em' },
    h2: { fontSize: '1.05rem', fontWeight: 600 },
  },
  components: {
    MuiTableCell: { styleOverrides: { root: { whiteSpace: 'nowrap' } } },
    MuiCard: { defaultProps: { variant: 'outlined' } },
  },
});

export default theme;
