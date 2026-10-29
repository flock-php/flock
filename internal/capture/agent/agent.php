<?php

// Flock's agent. Flock loads it with auto_prepend_file, next to the opentelemetry extension, while
// capture is on (see 90-flock-agent.ini). It sends dump() output and Laravel's queries, logs,
// views, jobs and requests to the Flock daemon, without the app installing anything.
//
// It runs before the app's autoloader, so it loads nothing itself: hook() lets it act once the
// app's own classes are in use (Application::boot, VarDumper::setHandler). It must run on every
// PHP the extension supports (8.0+), and must never break or slow down the app: everything it
// does is guarded, and when the daemon isn't listening it stops trying for that request.

namespace Flock\Agent;

use function OpenTelemetry\Instrumentation\hook;

// (Not class_exists(): the classes below are declared at compile time, before this line runs.)
if (\defined('FLOCK_AGENT') || !\function_exists('OpenTelemetry\Instrumentation\hook')) {
    return;
}
\define('FLOCK_AGENT', __FILE__);

final class Agent
{
    /** @var array<string, true> */
    public static array $kinds = [];
    public static bool $dumpsInPage = false;
    /** CLI workers (queue:work, ...) only report what happens inside a job. */
    public static bool $jobsOnly = false;
    public static bool $inJob = false;
    public static ?\WeakMap $apps = null;

    private static string $group = '';
    private static string $requestKey = '';
    /** @var array<int, float> job start times by job object id */
    private static array $jobStarts = [];

    public static function wants(string $kind): bool
    {
        return isset(self::$kinds[$kind]) && (!self::$jobsOnly || self::$inJob);
    }

    /** The request / CLI run (or, in a queue worker, the job) the current events belong to. */
    public static function group(): string
    {
        if (\PHP_SAPI !== 'cli') {
            // Long-running servers (Octane, FrankenPHP workers) handle many requests per process.
            $key = ($_SERVER['REQUEST_TIME_FLOAT'] ?? '').'|'.($_SERVER['REQUEST_URI'] ?? '');
            if ($key !== self::$requestKey) {
                self::$requestKey = $key;
                self::$group = '';
                Transport::reset();
            }
        }
        if (self::$group === '') {
            self::$group = \bin2hex(\random_bytes(8));
        }

        return self::$group;
    }

    public static function newGroup(): void
    {
        self::$group = '';
    }

    /** Fields every line carries: when, which request or command, and the group. */
    public static function origin(): array
    {
        $t = \microtime(true);
        $line = [
            'time' => \DateTimeImmutable::createFromFormat('U.u', \sprintf('%.6F', $t))->format('Y-m-d\TH:i:s.uP'),
            'group' => self::group(),
            'request' => null,
            'command_line' => null,
        ];
        if (\PHP_SAPI === 'cli') {
            $line['command_line'] = \implode(' ', $_SERVER['argv'] ?? []);
        } else {
            $https = !empty($_SERVER['HTTPS']) && \strtolower((string) $_SERVER['HTTPS']) !== 'off';
            $line['request'] = [
                'method' => (string) ($_SERVER['REQUEST_METHOD'] ?? 'GET'),
                'uri' => ($https ? 'https' : 'http').'://'.($_SERVER['HTTP_HOST'] ?? 'localhost').($_SERVER['REQUEST_URI'] ?? '/'),
            ];
        }

        return $line;
    }

    public static function emit(string $kind, array $fields): void
    {
        Transport::send(['kind' => $kind] + self::origin() + $fields);
    }

    /** The first frame outside vendor/ (and this file): the app code that caused an event. */
    public static function source(array $trace): ?array
    {
        foreach ($trace as $frame) {
            $file = $frame['file'] ?? '';
            if ($file === '' || $file === __FILE__ || \str_contains($file, \DIRECTORY_SEPARATOR.'vendor'.\DIRECTORY_SEPARATOR)) {
                continue;
            }

            return self::viewSource(['file' => $file, 'line' => (int) ($frame['line'] ?? 0), 'name' => \basename($file)]);
        }

        return null;
    }

    /**
     * Code in Blade templates runs from the compiled view in storage/framework/views. Laravel
     * appends the original path as a comment ("/**PATH ... ENDPATH**" + "/"), so map it back the
     * way Laravel's own dumper does. The compiled line number has no meaning there.
     */
    public static function viewSource(array $source): array
    {
        $file = $source['file'];
        if (\str_contains($file, \DIRECTORY_SEPARATOR.'framework'.\DIRECTORY_SEPARATOR.'views'.\DIRECTORY_SEPARATOR) && \is_file($file)
            && \preg_match('#/\*\*PATH (.+?) ENDPATH\*\*/#', (string) \file_get_contents($file), $m)) {
            return ['file' => $m[1], 'line' => 0, 'name' => \basename($m[1])];
        }

        return $source;
    }

    /** JSON-safe, size-limited copy of a value (log context, query bindings). */
    public static function plain(mixed $value, int $depth = 0): mixed
    {
        switch (true) {
            case $value === null || \is_bool($value) || \is_int($value) || \is_float($value):
                return $value;
            case \is_string($value):
                return \strlen($value) > 2000 ? \substr($value, 0, 2000).'…' : $value;
            case $value instanceof \Throwable:
                return \get_class($value).': '.$value->getMessage().' ('.$value->getFile().':'.$value->getLine().')';
            case $value instanceof \DateTimeInterface:
                return $value->format('Y-m-d H:i:s.uP');
            case $value instanceof \BackedEnum:
                return $value->value;
            case $value instanceof \UnitEnum:
                return \get_class($value).'::'.$value->name;
            case $value instanceof \Stringable:
                return self::plain((string) $value, $depth);
            case \is_array($value):
                if ($depth >= 3) {
                    return '[…]';
                }
                $out = [];
                foreach (\array_slice($value, 0, 50, true) as $k => $v) {
                    $out[$k] = self::plain($v, $depth + 1);
                }

                return $out;
            case \is_object($value):
                return \get_class($value);
            default:
                return \get_debug_type($value);
        }
    }

    /** Listens on a booted Laravel application's events, once per application. */
    public static function listen(object $app): void
    {
        if (isset(self::$apps[$app]) || !\method_exists($app, 'bound') || !$app->bound('events')) {
            return;
        }
        self::$apps[$app] = true;
        $events = $app['events'];

        // Each listener is guarded: the app must never notice the agent.
        $on = static function (string $event, callable $listener) use ($events): void {
            $events->listen($event, static function (...$args) use ($listener): void {
                try {
                    $listener(...$args);
                } catch (\Throwable) {
                }
            });
        };

        if (isset(self::$kinds['query'])) {
            $on('Illuminate\Database\Events\QueryExecuted', static function ($e): void {
                if (self::wants('query')) {
                    self::emit('query', [
                        'sql' => (string) $e->sql,
                        'bindings' => self::plain(\array_values((array) $e->bindings)),
                        'connection' => (string) $e->connectionName,
                        'duration_ms' => (float) $e->time,
                        'source' => self::source(\debug_backtrace(\DEBUG_BACKTRACE_IGNORE_ARGS, 40)),
                    ]);
                }
            });
        }
        if (isset(self::$kinds['log'])) {
            $on('Illuminate\Log\Events\MessageLogged', static function ($e): void {
                if (self::wants('log')) {
                    self::emit('log', [
                        'level' => (string) $e->level,
                        'message' => (string) self::plain((string) $e->message),
                        'context' => (object) self::plain((array) $e->context),
                        'source' => self::source(\debug_backtrace(\DEBUG_BACKTRACE_IGNORE_ARGS, 40)),
                    ]);
                }
            });
        }
        if (isset(self::$kinds['view'])) {
            $on('composing: *', static function ($event, $data): void {
                $view = $data[0] ?? null;
                // Laravel's error page renders hundreds of its own components (laravel-exceptions::,
                // laravel-exceptions-renderer::); they say nothing about the app.
                if (\str_starts_with((string) $event, 'composing: laravel-exceptions')) {
                    return;
                }
                if (self::wants('view') && \is_object($view) && \method_exists($view, 'name')) {
                    self::emit('view', [
                        'view' => (string) $view->name(),
                        'path' => \method_exists($view, 'getPath') ? (string) $view->getPath() : '',
                    ]);
                }
            });
        }

        // A queue worker reports from a job's start to its end; after a failure until it polls for
        // the next job (Looping), so the exception it reports after JobFailed lands in the job's group.
        $on('Illuminate\Queue\Events\JobProcessing', static function ($e): void {
            if (self::$jobsOnly) {
                self::newGroup();
                self::$inJob = true;
            }
            self::$jobStarts[\spl_object_id($e->job)] = \microtime(true);
            self::job('processing', $e);
        });
        $on('Illuminate\Queue\Events\JobProcessed', static function ($e): void {
            self::job('processed', $e);
            self::$inJob = false;
        });
        $on('Illuminate\Queue\Events\JobFailed', static function ($e): void {
            self::job('failed', $e, $e->exception ?? null);
        });
        $on('Illuminate\Queue\Events\Looping', static function (): void {
            self::$inJob = false;
        });

        if (isset(self::$kinds['request'])) {
            $on('Illuminate\Foundation\Http\Events\RequestHandled', static function ($e): void {
                $start = (float) ($_SERVER['REQUEST_TIME_FLOAT'] ?? \microtime(true));
                self::emit('request', [
                    'status_code' => (int) $e->response->getStatusCode(),
                    'duration_ms' => \round((\microtime(true) - $start) * 1000, 2),
                ]);
            });
        }
    }

    private static function job(string $state, object $e, ?\Throwable $exception = null): void
    {
        $id = \spl_object_id($e->job);
        $fields = [
            'job' => (string) $e->job->resolveName(),
            'queue' => (string) $e->job->getQueue(),
            'connection' => (string) ($e->connectionName ?? ''),
            'state' => $state,
        ];
        if ($state !== 'processing' && isset(self::$jobStarts[$id])) {
            $fields['duration_ms'] = \round((\microtime(true) - self::$jobStarts[$id]) * 1000, 2);
            unset(self::$jobStarts[$id]);
        }
        if ($exception !== null) {
            $fields['exception'] = \get_class($exception).': '.$exception->getMessage();
        }
        if (isset(self::$kinds['job']) && self::wants('job')) {
            self::emit('job', $fields);
        }
    }
}

/** Newline-delimited JSON to the daemon's collector; one connection per request or process. */
final class Transport
{
    public static string $address = '';
    /** @var resource|null */
    private static $socket = null;
    private static int $pid = 0;
    private static bool $down = false;
    private static bool $headerSent = false;

    /** A new request may try again after the daemon was down (long-running servers). */
    public static function reset(): void
    {
        self::$down = false;
    }

    public static function send(array $line): void
    {
        $json = \json_encode($line, \JSON_UNESCAPED_SLASHES | \JSON_UNESCAPED_UNICODE | \JSON_INVALID_UTF8_SUBSTITUTE | \JSON_PARTIAL_OUTPUT_ON_ERROR);
        if ($json === false || self::$down) {
            return;
        }
        // Twice: a kept-open connection may have been closed by a daemon restart.
        for ($try = 0; $try < 2; $try++) {
            $socket = self::socket();
            if ($socket === null) {
                return;
            }
            if (@\fwrite($socket, $json."\n") !== false) {
                return;
            }
            @\fclose($socket);
            self::$socket = null;
        }
    }

    /** Symfony's dump <script>/<style>, sent once per process for the UI to put above the dumps. */
    public static function sendHeader(string $html): void
    {
        if (!self::$headerSent) {
            self::$headerSent = true;
            self::send(['kind' => 'dump_header', 'html' => $html]);
        }
    }

    /** @return resource|null */
    private static function socket()
    {
        // A forked child (Horizon, pcntl) must not write into its parent's connection.
        if (self::$socket !== null && self::$pid === \getmypid()) {
            return self::$socket;
        }
        $socket = @\stream_socket_client('tcp://'.self::$address, $errno, $error, 0.05);
        if ($socket === false) {
            self::$down = true;
            self::$socket = null;

            return null;
        }
        \stream_set_timeout($socket, 1);
        self::$socket = $socket;
        self::$pid = \getmypid();

        return $socket;
    }
}

/** Sends every dump to Flock, then hands it to the handler it wraps: always for dd(), for dump() when asked to. */
final class DumpTee
{
    /** @var callable */
    private $inner;

    public function __construct(callable $inner)
    {
        $this->inner = $inner;
    }

    /** Symfony's default handler: dumps into the page, or the terminal on the command line. */
    public static function local(): callable
    {
        $cloner = new \Symfony\Component\VarDumper\Cloner\VarCloner();
        $dumper = \in_array(\PHP_SAPI, ['cli', 'phpdbg', 'embed'], true)
            ? new \Symfony\Component\VarDumper\Dumper\CliDumper()
            : new \Symfony\Component\VarDumper\Dumper\HtmlDumper();

        return static function (mixed $var, ?string $label = null) use ($cloner, $dumper): void {
            $data = $cloner->cloneVar($var);
            if ($label !== null && \method_exists($data, 'withContext')) {
                $data = $data->withContext(['label' => $label]);
            }
            $dumper->dump($data);
        };
    }

    public function __invoke(mixed $var, ?string $label = null): mixed
    {
        $dd = false;
        $source = null;
        // The outermost dump()/dd() frame with a file is where the app called it (also through
        // collect()->dump() and the Dumpable trait).
        foreach (\debug_backtrace(\DEBUG_BACKTRACE_IGNORE_ARGS, 12) as $frame) {
            $function = $frame['function'] ?? '';
            if ($function === 'dd' || $function === 'dump') {
                $dd = $dd || $function === 'dd';
                if (isset($frame['file'])) {
                    $source = ['file' => $frame['file'], 'line' => (int) ($frame['line'] ?? 0), 'name' => \basename($frame['file'])];
                }
            }
        }

        try {
            $data = (new \Symfony\Component\VarDumper\Cloner\VarCloner())->cloneVar($var);
            $html = new \Symfony\Component\VarDumper\Dumper\HtmlDumper();
            Transport::sendHeader((string) (function () {
                return $this->getDumpHeader();
            })->call($html));
            $html->setDumpHeader('');
            $text = new \Symfony\Component\VarDumper\Dumper\CliDumper();
            $text->setColors(false);

            Agent::emit('dump', [
                'label' => $label,
                'source' => $source !== null ? Agent::viewSource($source) : null,
                'text' => \rtrim((string) $text->dump($data, true)),
                'html' => (string) $html->dump($data, true),
            ]);
        } catch (\Throwable) {
        }

        if ($dd || Agent::$dumpsInPage) {
            return ($this->inner)($var, $label);
        }

        return null;
    }
}

(static function (): void {
    $address = (string) \get_cfg_var('flock.agent');
    $argv0 = \PHP_SAPI === 'cli' ? \basename((string) ($_SERVER['argv'][0] ?? '')) : '';
    // Composer is a tool, not the app: its runs aren't captured.
    if ($address === '' || \str_contains($argv0, 'composer')) {
        return;
    }
    Transport::$address = $address;
    Agent::$kinds = \array_fill_keys(\array_filter(\explode(',', (string) \get_cfg_var('flock.capture'))), true);
    // A shared site's visitors (FLOCK_SHARED, see `flock share`) never get dump() in the page.
    Agent::$dumpsInPage = (bool) \get_cfg_var('flock.dumps_in_page') && empty($_SERVER['FLOCK_SHARED']);
    Agent::$apps = new \WeakMap();

    if (\PHP_SAPI === 'cli' && $argv0 === 'artisan') {
        $command = (string) ($_SERVER['argv'][1] ?? '');
        Agent::$jobsOnly = \in_array($command, ['queue:work', 'queue:listen', 'horizon', 'horizon:work', 'horizon:supervisor'], true);
    }

    // PHP versions without the agent get dumps through the dump server (VAR_DUMPER_FORMAT=server).
    // Here the agent takes over: Laravel's own dumper must stay registered, so drop the setting.
    foreach (['VAR_DUMPER_FORMAT', 'VAR_DUMPER_SERVER'] as $name) {
        unset($_SERVER[$name], $_ENV[$name]);
        \putenv($name);
    }

    // Whoever installs a dump handler (Laravel, Symfony itself, Ignition, Telescope), wrap it.
    if (isset(Agent::$kinds['dump'])) {
        hook('Symfony\Component\VarDumper\VarDumper', 'setHandler', static function ($class, array $params): ?array {
            $handler = $params[0] ?? null;
            if ($handler === null || $handler instanceof DumpTee || !\is_callable($handler)) {
                return null;
            }
            // VAR_DUMPER_FORMAT=server can come back from the app's .env (Flock suggested it for the
            // dump server): Symfony then installs a handler that sends to the dump server instead of
            // the page. The agent has the dump already, so render dd() (and dump() when asked) here.
            $format = (string) ($_SERVER['VAR_DUMPER_FORMAT'] ?? '');
            if ($format === 'server' || \str_starts_with($format, 'tcp://')) {
                $handler = DumpTee::local();
            }

            return [0 => new DumpTee($handler)];
        });
    }

    // Laravel: once the application has booted, listen on its events.
    hook('Illuminate\Foundation\Application', 'boot', null, static function ($app): void {
        try {
            Agent::listen($app);
        } catch (\Throwable) {
        }
    });
})();

// The app's own auto_prepend_file (from php.ini), which Flock's ini replaced with this file.
if (($prepend = (string) \get_cfg_var('flock.user_prepend')) !== '' && \is_file($prepend)) {
    require $prepend;
}
unset($prepend);
