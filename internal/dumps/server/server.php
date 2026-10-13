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

$server = new DumpServer($host);
$server->start();
fwrite(STDERR, "flock dump server listening on {$host}\n");

$sequence = 0;

$server->listen(function (Data $data, array $context, int $clientId) use ($output, &$sequence): void {
    $timestamp = (float) ($context['timestamp'] ?? microtime(true));

    // A fresh HtmlDumper per dump includes its own <style>/<script> header, so every entry renders standalone.
    $html = (new HtmlDumper())->dump($data, true);

    $cli = new CliDumper();
    $cli->setColors(false);
    $text = $cli->dump($data, true);

    $source = $context['source'] ?? null;
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
        'text' => rtrim($text),
        'html' => $html,
    ];

    file_put_contents(
        $output,
        json_encode($entry, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE)."\n",
        FILE_APPEND | LOCK_EX,
    );
});
