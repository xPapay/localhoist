<?php

namespace Localhoist\Laravel;

use Illuminate\Contracts\Http\Kernel;
use Illuminate\Support\ServiceProvider;

class ShareServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        if ($this->app->runningInConsole()) {
            $this->commands([ShareCommand::class]);

            return;
        }

        // Appended (not prepended) so it runs after the framework's own
        // TrustProxies, which resets trusted proxies at the front of the
        // stack. Local environment only: on a production box behind a
        // loopback reverse proxy, a forwarded marker header must not
        // grant trust.
        if ($this->app->environment('local')) {
            $this->app->make(Kernel::class)->pushMiddleware(TrustLocalhoistProxy::class);
        }
    }
}
