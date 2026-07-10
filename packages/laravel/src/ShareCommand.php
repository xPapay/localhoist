<?php

namespace Localhoist\Laravel;

use Illuminate\Console\Command;
use RuntimeException;
use Symfony\Component\Process\Exception\RuntimeException as ProcessRuntimeException;
use Symfony\Component\Process\Process;

class ShareCommand extends Command
{
    protected $signature = 'share
        {--domain= : Static tunnel domain (e.g. my-app.ngrok-free.dev)}
        {--no-qr : Skip the QR code}
        {--no-env-patch : Do not touch .env (URLs/websockets may break)}
        {--env-patch : Patch .env even though the middleware handles URLs (e.g. for links in queued emails)}
        {--binary= : Path to the localhoist binary (overrides auto-detection)}';

    protected $description = 'Put this app online — Vite HMR, Reverb websockets, and signed URLs all working through one tunnel';

    public function handle(BinaryLocator $locator): int
    {
        if ($this->runningInsideContainer()) {
            $this->warn('This command appears to be running inside a container (Sail?).');
            $this->warn('The tunnel must run where your ports are published — on the host.');
            $this->warn('Install the localhoist binary on your host and run it there instead.');
            $this->newLine();
        }

        try {
            $binary = $this->option('binary') ?: $locator->find(fn (string $m) => $this->info($m));
        } catch (RuntimeException $e) {
            $this->error($e->getMessage());

            return self::FAILURE;
        }

        $process = new Process(
            $this->binaryArgs($binary),
            base_path(),
            null,
            null,
            null // the tunnel runs until the user stops it
        );

        // A TTY hands the binary the real terminal: live output, QR code,
        // and Ctrl+C delivered straight to it (it restores .env on the way
        // out). Fall back to streaming output where no TTY exists.
        try {
            $process->setTty(true);
        } catch (ProcessRuntimeException) {
            // not a TTY (CI, piped output) — stream instead
        }

        return $process->run(fn ($type, string $buffer) => $this->output->write($buffer));
    }

    /** @return list<string> */
    private function binaryArgs(string $binary): array
    {
        $args = [$binary, '--dir', base_path()];

        if ($domain = $this->option('domain')) {
            $args[] = '--domain='.$domain;
        }
        if ($this->option('no-qr')) {
            $args[] = '--no-qr';
        }
        if ($this->option('no-env-patch')) {
            $args[] = '--no-env-patch';
        }
        if ($this->option('env-patch')) {
            $args[] = '--env-patch';
        }

        return $args;
    }

    private function runningInsideContainer(): bool
    {
        return getenv('LARAVEL_SAIL') === '1'
            || file_exists('/.dockerenv')
            || file_exists('/run/.containerenv');
    }
}
