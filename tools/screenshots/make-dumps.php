<?php

// Writes the Dumps window's fixtures (dumps.json, dumps_header.html) with the dump server's own
// copy of symfony/var-dumper, so they look exactly like captured dumps, and the queries, logs,
// views, jobs and requests captured next to them (events.json):
//
//   php tools/screenshots/make-dumps.php

namespace App\Models {
    class User
    {
        public int $id = 42;
        public string $name = 'Ada Lovelace';
        public string $email = 'ada@acme.test';
        public array $roles = ['admin', 'billing'];
        public ?string $email_verified_at = '2026-03-14 09:26:53';
    }

    class Order
    {
        public int $id = 1042;
        public string $status = 'paid';
        public float $total = 129.9;
        public array $lines = [
            ['sku' => 'ACME-MUG', 'quantity' => 2, 'price' => 14.95],
            ['sku' => 'ACME-HOODIE', 'quantity' => 1, 'price' => 100.0],
        ];
    }
}

namespace {
    require __DIR__.'/../../internal/dumps/server/vendor/autoload.php';

    use Symfony\Component\VarDumper\Cloner\VarCloner;
    use Symfony\Component\VarDumper\Dumper\CliDumper;
    use Symfony\Component\VarDumper\Dumper\HtmlDumper;

    final class FixtureHtmlDumper extends HtmlDumper
    {
        public function header(): string
        {
            return $this->getDumpHeader();
        }
    }

    $fixtures = __DIR__.'/fixtures';
    file_put_contents("$fixtures/dumps_header.html", (new FixtureHtmlDumper())->header());

    $request = ['method' => 'GET', 'uri' => 'https://acme.test/orders/1042'];
    $controller = fn (int $line): array => ['file' => 'C:\\Users\\demo\\Code\\acme\\app\\Http\\Controllers\\OrderController.php', 'line' => $line, 'name' => 'OrderController.php'];
    $import = fn (int $line): array => ['file' => 'C:\\Users\\demo\\Code\\acme\\app\\Console\\Commands\\ImportProducts.php', 'line' => $line, 'name' => 'ImportProducts.php'];
    $dumps = [
        // Oldest first; the daemon lists them newest first (see below).
        ['ago' => '9m', 'value' => ['imported' => 128, 'skipped' => 3, 'took' => '1.84s'], 'label' => null,
            'source' => ['file' => 'C:\\Users\\demo\\Code\\acme\\app\\Console\\Commands\\ImportProducts.php', 'line' => 57, 'name' => 'ImportProducts.php'],
            'request' => null, 'command_line' => 'php artisan products:import', 'group' => 'cli-1'],
        ['ago' => '44s', 'value' => new App\Models\User(), 'label' => null,
            'source' => $controller(31),
            'request' => $request, 'command_line' => null, 'group' => 'request-3'],
        ['ago' => '40s', 'value' => new App\Models\Order(), 'label' => 'order',
            'source' => $controller(37),
            'request' => $request, 'command_line' => null, 'group' => 'request-3'],
    ];

    // Oldest first, like the dumps; times between the dumps' put each in its place in the timeline.
    $events = [
        ['ago' => '9m', 'kind' => 'query', 'group' => 'cli-1', 'source' => $import(41),
            'sql' => 'select * from "categories" where "slug" in (?, ?, ?)', 'bindings' => ['mugs', 'hoodies', 'stickers'], 'connection' => 'pgsql', 'duration_ms' => 1.12],
        ['ago' => '9m', 'kind' => 'log', 'group' => 'cli-1', 'source' => $import(55),
            'level' => 'warning', 'message' => 'Skipped 3 products without a price', 'context' => ['skus' => ['ACME-CAP', 'ACME-PIN', 'ACME-TOTE']]],
        ['ago' => '2m', 'kind' => 'job', 'group' => 'job-2', 'job' => 'App\\Jobs\\SendOrderConfirmation', 'queue' => 'default', 'connection' => 'database', 'state' => 'processing'],
        ['ago' => '2m', 'kind' => 'query', 'group' => 'job-2', 'source' => ['file' => 'C:\\Users\\demo\\Code\\acme\\app\\Jobs\\SendOrderConfirmation.php', 'line' => 28, 'name' => 'SendOrderConfirmation.php'],
            'sql' => 'select * from "orders" where "id" = ? limit 1', 'bindings' => [1041], 'connection' => 'pgsql', 'duration_ms' => 0.44],
        ['ago' => '2m', 'kind' => 'job', 'group' => 'job-2', 'job' => 'App\\Jobs\\SendOrderConfirmation', 'queue' => 'default', 'connection' => 'database', 'state' => 'processed', 'duration_ms' => 212.6],
        ['ago' => '45s', 'kind' => 'query', 'group' => 'request-3', 'source' => $controller(29),
            'sql' => 'select * from "orders" where "id" = ? and "user_id" = ? limit 1', 'bindings' => [1042, 42], 'connection' => 'pgsql', 'duration_ms' => 0.58],
        ['ago' => '43s', 'kind' => 'query', 'group' => 'request-3', 'source' => $controller(33),
            'sql' => 'select * from "order_lines" where "order_lines"."order_id" in (?)', 'bindings' => [1042], 'connection' => 'pgsql', 'duration_ms' => 0.37],
        ['ago' => '42s', 'kind' => 'log', 'group' => 'request-3', 'source' => $controller(35),
            'level' => 'info', 'message' => 'Order 1042 viewed', 'context' => ['order_id' => 1042, 'user_id' => 42]],
        ['ago' => '41s', 'kind' => 'view', 'group' => 'request-3', 'view' => 'orders.show', 'path' => 'C:\\Users\\demo\\Code\\acme\\resources\\views\\orders\\show.blade.php'],
        ['ago' => '41s', 'kind' => 'view', 'group' => 'request-3', 'view' => 'components.layouts.app', 'path' => 'C:\\Users\\demo\\Code\\acme\\resources\\views\\components\\layouts\\app.blade.php'],
        ['ago' => '39s', 'kind' => 'request', 'group' => 'request-3', 'status_code' => 200, 'duration_ms' => 84.3],
    ];
    $origins = [
        'cli-1' => ['request' => null, 'command_line' => 'php artisan products:import'],
        'job-2' => ['request' => null, 'command_line' => 'php artisan queue:work'],
        'request-3' => ['request' => $request, 'command_line' => null],
    ];
    $records = [];
    foreach ($events as $index => $event) {
        $records[] = ['id' => sprintf('event-%d', $index + 1), 'kind' => $event['kind'], 'time' => '{{now-'.$event['ago'].'}}', 'site' => 'acme', 'group' => $event['group']]
            + $origins[$event['group']]
            + ['source' => $event['source'] ?? null]
            + array_diff_key($event, ['ago' => 0, 'kind' => 0, 'group' => 0, 'source' => 0]);
    }
    file_put_contents("$fixtures/events.json", json_encode(array_reverse($records), JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)."\n");

    $entries = [];
    foreach ($dumps as $index => $dump) {
        $data = (new VarCloner())->cloneVar($dump['value']);
        if ($dump['label'] !== null) {
            $data = $data->withContext(['label' => $dump['label']]);
        }
        $html = new HtmlDumper();
        $html->setDumpHeader('');
        $cli = new CliDumper();
        $cli->setColors(false);

        $entries[] = [
            'id' => sprintf('fixture-%d', $index + 1),
            'time' => '{{now-'.$dump['ago'].'}}',
            'label' => $dump['label'],
            'site' => 'acme',
            'source' => $dump['source'],
            'request' => $dump['request'],
            'command_line' => $dump['command_line'],
            'group' => $dump['group'],
            'text' => rtrim($cli->dump($data, true)),
            'html' => $html->dump($data, true),
        ];
    }

    file_put_contents("$fixtures/dumps.json", json_encode(array_reverse($entries), JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)."\n");
}
