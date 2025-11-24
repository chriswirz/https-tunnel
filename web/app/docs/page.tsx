'use client';

import * as React from 'react';
import Link from 'next/link';
import {
  Box,
  Card,
  CardContent,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Typography,
} from '@mui/material';

function Code({ children }: { children: React.ReactNode }) {
  return (
    <Card>
      <CardContent
        component="pre"
        sx={{ m: 0, fontFamily: 'ui-monospace, monospace', fontSize: '0.82rem', overflowX: 'auto' }}
      >
        {children}
      </CardContent>
    </Card>
  );
}

const ENDPOINTS: [string, string, string][] = [
  ['POST', '/api/v1/connect', 'register or resume a session'],
  ['GET', '/api/v1/tunnel', 'upgrade to the tunnel protocol'],
  ['GET', '/api/v1/sessions', 'list every session'],
  ['GET', '/api/v1/sessions/{id}', 'fetch one session'],
  ['DELETE', '/api/v1/sessions/{id}', 'delete a session and disconnect its client'],
  ['GET', '/healthz', 'liveness probe'],
  ['GET', '/openapi.json', 'this API as OpenAPI 3.1'],
];

export default function DocsPage() {
  const [host, setHost] = React.useState('tunnel.example.com');
  React.useEffect(() => setHost(window.location.host), []);

  return (
    <Stack spacing={3}>
      <div>
        <Typography variant="h1">How it works</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 680 }}>
          One binary, two roles. The client opens a single long lived connection outbound; the server multiplexes public
          HTTP requests back down it. The full API is described by <a href="/openapi.json">/openapi.json</a> and
          rendered as <Link href="/swagger">Swagger UI</Link>.
        </Typography>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          1. Connect
        </Typography>
        <Code>{`POST /api/v1/connect
Authorization: Bearer <api key>
{"session_id": "<optional, to resume>"}

200 OK
{"session": "<id>", "url": "https://<label>.${host}"}`}</Code>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          2. Attach the tunnel
        </Typography>
        <Code>{`GET /api/v1/tunnel
Authorization: Bearer <api key>
X-Tunnel-Session: <session id>
Connection: Upgrade
Upgrade: https-tunnel

101 Switching Protocols`}</Code>
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, maxWidth: 680 }}>
          After the upgrade both sides speak the binary frame protocol: a one byte type, an eight byte stream id, a four
          byte length, then the payload. Each public request gets its own stream id, so requests and streaming responses
          interleave freely on the one socket.
        </Typography>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          3. Serve
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 680 }}>
          Requests to <code>https://&lt;label&gt;.{host}</code> are framed, sent to the client, replayed against the
          local port, and streamed back. Response bodies are flushed chunk by chunk, so server-sent events and other
          streaming MCP transports work unbuffered.
        </Typography>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          Endpoints
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5, maxWidth: 680 }}>
          All of these live on this hostname only. A <code>&lt;label&gt;.{host}</code> hostname is a tunnel and serves
          whatever its client is running, so none of these paths exist there.
        </Typography>
        <Box sx={{ overflowX: 'auto' }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>method</TableCell>
                <TableCell>path</TableCell>
                <TableCell>what it does</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {ENDPOINTS.map(([method, path, what]) => (
                <TableRow key={method + path}>
                  <TableCell>{method}</TableCell>
                  <TableCell>
                    <code>{path}</code>
                  </TableCell>
                  <TableCell>{what}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Box>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          Client configuration
        </Typography>
        <Code>{`{
  "client": {
    "api_key": "...",
    "server_url": "https://${host}",
    "session_id": "",
    "local_port": 8756
  }
}`}</Code>
      </div>
    </Stack>
  );
}
