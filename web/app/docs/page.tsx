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

// Prose and code share one measure, so the page reads as a single column rather
// than paragraphs indented inside wider slabs of code.
const COLUMN = 680;

function Code({ children }: { children: React.ReactNode }) {
  return (
    <Card sx={{ maxWidth: COLUMN }}>
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
  ['DELETE', '/api/v1/sessions?idle_for=24h', 'delete every disconnected session idle that long'],
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
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: COLUMN }}>
          This server gives a local HTTP service a public HTTPS address. Anything that speaks HTTP works: a web app you
          are building, a webhook receiver that needs a real URL, a REST or gRPC-web API, a static site, a dashboard on a
          machine with no inbound access, an MCP server. One binary, two roles: the client opens a single long lived
          connection outbound, and the server multiplexes public requests back down it. The full API is described by{' '}
          <a href="/openapi.json">/openapi.json</a> and rendered as <Link href="/swagger">Swagger UI</Link>.
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
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, maxWidth: COLUMN }}>
          After the upgrade both sides speak the binary frame protocol: a one byte type, an eight byte stream id, a four
          byte length, then the payload. Each public request gets its own stream id, so requests and streaming responses
          interleave freely on the one socket.
        </Typography>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          3. Serve
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: COLUMN }}>
          Requests to <code>https://&lt;label&gt;.{host}</code> are framed, sent to the client, replayed against
          whatever it is exposing, and streamed back. Method, path, query and headers all survive the trip. Response
          bodies are flushed chunk by chunk, so server-sent events, chunked responses and long polling work unbuffered
          rather than arriving in one lump at the end.
        </Typography>
      </div>

      <div>
        <Typography variant="h2" gutterBottom>
          Endpoints
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5, maxWidth: COLUMN }}>
          All of these live on this hostname only. A <code>&lt;label&gt;.{host}</code> hostname is a tunnel and serves
          whatever its client is running, so none of these paths exist there.
        </Typography>
        <Box sx={{ overflowX: 'auto', maxWidth: COLUMN }}>
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
          What a client can expose
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5, maxWidth: COLUMN }}>
          Three shapes, one of which every client picks:
        </Typography>
        <Box sx={{ overflowX: 'auto', maxWidth: COLUMN }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>setting</TableCell>
                <TableCell>what it does</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              <TableRow>
                <TableCell>
                  <code>local_port</code>
                </TableCell>
                <TableCell>proxy to a server already listening on this machine</TableCell>
              </TableRow>
              <TableRow>
                <TableCell>
                  <code>local_dir</code>
                </TableCell>
                <TableCell>serve a folder from disk, with no local server at all</TableCell>
              </TableRow>
              <TableRow>
                <TableCell>
                  <code>Handler</code>
                </TableCell>
                <TableCell>
                  an <code>http.Handler</code> inside your own program, using the client as a library
                </TableCell>
              </TableRow>
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
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, maxWidth: COLUMN }}>
          8756 is just an example; any port this machine can reach works, including one in another container.
        </Typography>
      </div>
    </Stack>
  );
}
