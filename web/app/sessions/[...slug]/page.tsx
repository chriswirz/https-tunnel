import SessionClient from './SessionClient';

// A static export cannot know the session ids ahead of time, so one placeholder
// page is generated and the Go server serves it for every /sessions/{id} URL.
// The client component reads the real id back out of the address bar.
export function generateStaticParams() {
  return [{ slug: ['__placeholder__'] }];
}

export const dynamicParams = false;

export default function SessionPage() {
  return <SessionClient />;
}
