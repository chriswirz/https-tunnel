/** @type {import('next').NextConfig} */
// Static export, so the entire frontend can be embedded in the Go binary with
// go:embed. distDir stays at the default (.next) and the export lands in out/.
const isProd = process.env.NODE_ENV === 'production';

const nextConfig = {
  output: isProd ? 'export' : undefined,
  images: { unoptimized: true },
  trailingSlash: false,
  // In `next dev` the Go server runs separately on 8080, so the API is proxied
  // to it and the frontend behaves exactly as it does once embedded. The key is
  // omitted entirely in production, where a static export cannot rewrite.
  ...(isProd
    ? {}
    : {
        async rewrites() {
          return [
            { source: '/api/:path*', destination: 'http://localhost:8080/api/:path*' },
            { source: '/openapi.json', destination: 'http://localhost:8080/openapi.json' },
            { source: '/healthz', destination: 'http://localhost:8080/healthz' },
          ];
        },
      }),
};
module.exports = nextConfig;
