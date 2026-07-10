<?php

namespace Localhoist\Laravel;

use Closure;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Request as SymfonyRequest;
use Symfony\Component\HttpFoundation\Response;

/**
 * Trusts the localhoist mux as a proxy — but only for requests that
 * actually came through it: connected from loopback AND carrying the
 * marker header the mux stamps on every proxied request.
 *
 * With the proxy trusted, Laravel honors the X-Forwarded-Proto/Host that
 * the tunnel edge set, so url(), asset(), redirects, and signed URLs are
 * all generated against the public https origin — with .env untouched.
 *
 * Registered only in the `local` environment (see ShareServiceProvider);
 * direct local requests without the marker are unaffected either way.
 */
class TrustLocalhoistProxy
{
    public const MARKER_HEADER = 'X-Localhoist';

    public function handle(Request $request, Closure $next): Response
    {
        if ($this->throughLocalhoist($request)) {
            Request::setTrustedProxies(
                [$request->server->get('REMOTE_ADDR')],
                SymfonyRequest::HEADER_X_FORWARDED_FOR
                    | SymfonyRequest::HEADER_X_FORWARDED_HOST
                    | SymfonyRequest::HEADER_X_FORWARDED_PORT
                    | SymfonyRequest::HEADER_X_FORWARDED_PROTO
            );
        }

        return $next($request);
    }

    private function throughLocalhoist(Request $request): bool
    {
        return in_array($request->server->get('REMOTE_ADDR'), ['127.0.0.1', '::1'], true)
            && $request->headers->has(self::MARKER_HEADER);
    }
}
