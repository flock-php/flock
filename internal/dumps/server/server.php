<?php

// Flock dump server. Apps run with VAR_DUMPER_FORMAT=server send every dump() here
// (Symfony's ServerDumper protocol); each one is rendered to HTML and plain text and
// appended as a JSON line to the output file, which the Flock daemon serves to its UI.
//
// Usage: php server.php <host:port> <output.jsonl>

declare(strict_types=1);

require __DIR__.'/vendor/autoload.php';

use Symfony\Component\VarDumper\Cloner\Data;
use Symfony\Component\VarDumper\Dumper\CliDumper;
use Symfony\Component\VarDumper\Dumper\HtmlDumper;
use Symfony\Component\VarDumper\Server\DumpServer;

[, $host, $output] = $argv + [null, '127.0.0.1:9912', 'php://stdout'];

/**
 * Exposes Symfony's dump header (the <script>/<style> that makes dumps collapsible), so it
 * can be stored once instead of being repeated in every entry.
 */
final class FlockHtmlDumper extends HtmlDumper
{
    public function header(): string
    {
        return $this->getDumpHeader();
    }
}

// The header goes next to the output; the UI loads it once for the whole timeline.
file_put_contents(dirname($output).'/header.html', (new FlockHtmlDumper())->header());

$server = new DumpServer($host);
$server->start();
fwrite(STDERR, "flock dump server listening on {$host}\n");

/**
 * Dumps from Blade templates point at the compiled view in storage/framework/views.
 * Laravel appends the original path as a comment ("/**PATH ... ENDPATH**" + "/"), so map it back
 * the same way Laravel's own dumper does. The compiled line number has no meaning there.
 */
function originalViewSource(array $source): array
{
    $file = (string) ($source['file'] ?? '');
    if (!str_contains($file, '/storage/framework/views/') || !is_file($file)) {
        return $source;
    }
    if (preg_match('#/\*\*PATH (.+?) ENDPATH\*\*/#', (string) file_get_contents($file), $m)) {
        return ['file' => $m[1], 'line' => 0, 'name' => basename($m[1])];
    }

    return $source;
}

$sequence = 0;

$server->listen(function (Data $data, array $context, int $clientId) use ($output, &$sequence): void {
    $timestamp = (float) ($context['timestamp'] ?? microtime(true));

    // Body only: the shared header is in header.html.
    $htmlDumper = new HtmlDumper();
    $htmlDumper->setDumpHeader('');
    $html = $htmlDumper->dump($data, true);

    $cli = new CliDumper();
    $cli->setColors(false);
    $text = $cli->dump($data, true);

    $source = $context['source'] ?? null;
    if ($source) {
        $source = originalViewSource($source);
    }
    $request = $context['request'] ?? null;

    $entry = [
        'id' => sprintf('%d-%06d', (int) ($timestamp * 1_000_000), ++$sequence),
        'time' => DateTimeImmutable::createFromFormat('U.u', sprintf('%.6F', $timestamp))->format(DATE_RFC3339_EXTENDED),
        'label' => $data->getContext()['label'] ?? null,
        'source' => $source ? [
            'file' => (string) ($source['file'] ?? ''),
            'line' => (int) ($source['line'] ?? 0),
            'name' => (string) ($source['name'] ?? ''),
        ] : null,
        'request' => $request ? [
            'method' => (string) ($request['method'] ?? ''),
            'uri' => (string) ($request['uri'] ?? ''),
        ] : null,
        'command_line' => $context['cli']['command_line'] ?? null,
        // Same value for every dump of one HTTP request or CLI run, so the UI can show "latest request only".
        'group' => (string) ($request['identifier'] ?? $context['cli']['identifier'] ?? $clientId),
        'text' => rtrim($text),
        'html' => $html,
    ];

    file_put_contents(
        $output,
        json_encode($entry, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE)."\n",
        FILE_APPEND | LOCK_EX,
    );
});
