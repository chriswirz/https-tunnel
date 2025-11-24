'use client';

import * as React from 'react';
import dynamic from 'next/dynamic';
import { Box, Stack, Typography } from '@mui/material';
import 'swagger-ui-react/swagger-ui.css';
import './swagger-theme.css';

// Swagger UI touches window on import, so it is loaded client side only. The
// bundle ships inside the binary with the rest of the export, which keeps this
// page working on a server with no outbound internet.
const SwaggerUI = dynamic(() => import('swagger-ui-react'), {
  ssr: false,
  loading: () => <Typography variant="body2">Loading the API reference...</Typography>,
});

export default function SwaggerPage() {
  return (
    <Stack spacing={2}>
      <div>
        <Typography variant="h1">API reference</Typography>
        <Typography variant="body2" color="text.secondary">
          Rendered from <a href="/openapi.json">/openapi.json</a>. Sign in first, then the endpoints can be called from
          here.
        </Typography>
      </div>
      {/* The palette lives in swagger-theme.css, which repaints Swagger's own
          stylesheet from this application's CSS variables. */}
      <Box>
        <SwaggerUI url="/openapi.json" docExpansion="list" persistAuthorization tryItOutEnabled />
      </Box>
    </Stack>
  );
}
