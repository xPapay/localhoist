<?php

namespace Localhoist\Laravel;

use Illuminate\Console\Command;
use RuntimeException;
use Symfony\Component\Process\Exception\RuntimeException as ProcessRuntimeException;
use Symfony\Component\Process\Process;

class ShareCommand extends Command
{
    protected $signature = 'share
        {--transport= : Tunnel transport: cloudflare (quick tunnel, default) or ngrok}
        {--domain= : Static tunnel domain, ngrok only (e.g. my-app.ngrok-free.dev)}
        {--no-qr : Skip the QR code}
        {--no-env-patch : Do not touch .env (URLs/websockets may break)}
        {--env-patch : Patch .env even though the middleware handles URLs (e.g. for links in queued emails)}
        {--binary= : Path to the localhoist binary (overrides auto-detection)}
        {--force : Run even inside a container (advanced — the tunnel still needs host-published ports)}';

    protected $description = 'Put this app online — Vite HMR, Reverb websockets, and signed URLs all working through one tunnel';

    public function handle(BinaryLocator $locator): int
    {
        // Sail/Docker: artisan runs inside the container, but the tunnel must
        // run on the host where the ports are published. Redirect there rather
        // than run and fail confusingly. --force is the escape hatch for
        // non-standard setups that publish ports differently.
        if ($this->runningInsideContainer() && ! $this->option('force')) {
            return $this->guideToHost();
        }

        if ($this->runningInsideContainer()) {
            $this->warn('Running inside a container because --force was passed — this only works if');
            $this->warn("the host's published ports are reachable from here, or the tunnel will fail.");
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

        if ($transport = $this->option('transport')) {
            $args[] = '--transport='.$transport;
        }
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

    /**
     * Point the user at the host, where the tunnel actually has to run.
     * Note: we don't echo base_path() — inside the container that's the
     * container path, not the host project path the user needs.
     */
    private function guideToHost(): int
    {
        $this->error("php artisan share can't open the tunnel from inside a container.");
        $this->newLine();
        $this->line('  The tunnel runs on your host, where your ports are published.');
        $this->line('  In a host terminal (not the Sail shell), from your project directory:');
        $this->newLine();
        $this->info('      localhoist');
        $this->newLine();
        $this->line('  First time?           brew install xPapay/tap/localhoist');
        $this->line('  Sure this is right?   php artisan share --force');
        $this->newLine();

        return self::FAILURE;
    }
}
