// Shapes returned by the https-tunnel control plane.
// These mirror internal/server/openapi.json; `npm run codegen` regenerates a
// full typed client from that document into lib/api.gen.ts when it is wanted.

export interface Session {
  session: string;
  subdomain: string;
  url: string;
  key_name: string;
  connected: boolean;
  created_at: string;
  last_seen: string;
  requests: number;
  remote_addr?: string;
}

export interface SessionList {
  sessions: Session[];
}

export interface ApiError {
  error: string;
}

export interface AuthStatus {
  authenticated: boolean;
  username?: string;
  must_change_password: boolean;
  /** True while the server has no administrator password, so the login form can say so. */
  password_unset: boolean;
}
