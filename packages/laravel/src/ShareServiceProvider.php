<?php

namespace Localhoist\Laravel;

use Illuminate\Support\ServiceProvider;

class ShareServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        if ($this->app->runningInConsole()) {
            $this->commands([ShareCommand::class]);
        }
    }
}
