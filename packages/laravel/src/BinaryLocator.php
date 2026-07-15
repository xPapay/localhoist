<?php

namespace Localhoist\Laravel;

use Closure;
use RuntimeException;
use Symfony\Component\Process\ExecutableFinder;

/**
 * Finds the localhoist binary, downloading a platform build on first use
 * (the tailwindcss-standalone pattern).
 *
 * Resolution order: $LOCALHOIST_BINARY → `localhoist` on PATH → cached download
 * → fresh download from the GitHub release matching VERSION.
 */
class BinaryLocator
{
    /** The binary release this wrapper version is pinned to. */
    public const VERSION = '0.3.0';

    /** %s placeholders: version, asset name. */
    private const RELEASE_URL = 'https://github.com/xPapay/localhoist/releases/download/v%s/%s';

    /**
     * @param Closure(string): void|null $status progress messages for the console
     */
    public function find(?Closure $status = null): string
    {
        if ($env = getenv('LOCALHOIST_BINARY')) {
            if (! is_executable($env)) {
                throw new RuntimeException("LOCALHOIST_BINARY points to {$env}, which is not an executable file.");
            }

            return $env;
        }

        if ($path = (new ExecutableFinder)->find('localhoist')) {
            return $path;
        }

        $cached = $this->cachePath();

        if (is_executable($cached)) {
            return $cached;
        }

        return $this->download($cached, $status);
    }

    /** Where the downloaded binary lives, versioned per platform. */
    public function cachePath(): string
    {
        [$os, $arch] = $this->platform();
        $home = getenv('HOME') ?: sys_get_temp_dir();

        return $home.'/.localhoist/bin/'.$this->assetName($os, $arch);
    }

    /** @return array{0: string, 1: string} */
    private function platform(): array
    {
        $os = match (PHP_OS_FAMILY) {
            'Darwin' => 'darwin',
            'Windows' => 'windows',
            default => 'linux',
        };

        $machine = strtolower(php_uname('m'));
        $arch = in_array($machine, ['arm64', 'aarch64']) ? 'arm64' : 'amd64';

        return [$os, $arch];
    }

    private function assetName(string $os, string $arch): string
    {
        return sprintf('localhoist-%s-%s-%s%s', self::VERSION, $os, $arch, $os === 'windows' ? '.exe' : '');
    }

    private function download(string $to, ?Closure $status): string
    {
        [$os, $arch] = $this->platform();
        $url = sprintf(self::RELEASE_URL, self::VERSION, $this->assetName($os, $arch));

        if ($status) {
            $status(sprintf('Downloading localhoist %s for %s/%s …', self::VERSION, $os, $arch));
        }

        if (! is_dir(dirname($to))) {
            mkdir(dirname($to), 0755, true);
        }

        $partial = $to.'.partial';
        $in = @fopen($url, 'rb');

        if ($in === false) {
            throw new RuntimeException(
                "Could not download the localhoist binary from {$url}.\n".
                'If no release exists for your platform yet: build it from source '.
                '(`go build ./cmd/localhoist`) and put `localhoist` on your PATH, or point '.
                'the LOCALHOIST_BINARY environment variable at it.'
            );
        }

        $out = fopen($partial, 'wb');
        stream_copy_to_stream($in, $out);
        fclose($in);
        fclose($out);

        chmod($partial, 0755);
        rename($partial, $to);

        return $to;
    }
}
