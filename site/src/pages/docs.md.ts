// The same page, as the bytes it was written in.
//
// This is the affordance that actually works for an agent. Content negotiation
// on `Accept: text/markdown` is the other half of the spec and cannot be done
// here at all: Pages is a static host with no request-time logic, so a URL that
// ends in .md is the only lever available.
import type { APIRoute } from 'astro';
import raw from '../content/docs.md?raw';

export const GET: APIRoute = () =>
  new Response(raw, {
    headers: {
      'content-type': 'text/markdown; charset=utf-8',
      'cache-control': 'public, max-age=600',
    },
  });
