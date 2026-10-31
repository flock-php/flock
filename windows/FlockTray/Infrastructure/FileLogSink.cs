using System.Threading.Channels;

namespace Flock.Tray.Infrastructure;

/// <summary>Queues redacted log records to one bounded writer without blocking the UI thread.</summary>
public sealed class FileLogSink : IAsyncDisposable
{
    private readonly Channel<string> records = Channel.CreateBounded<string>(new BoundedChannelOptions(256) { FullMode = BoundedChannelFullMode.Wait, SingleReader = true });
    private readonly string path;
    private readonly CancellationTokenSource stop = new();
    private readonly Task writer;
    private readonly object drainGate = new();
    private TaskCompletionSource drained = CompletedDrain();
    private int pending;
    private long overflow;
    private long reportedOverflow;
    private int unavailable;
    private int disposed;

    public FileLogSink(string? path = null)
    {
        this.path = path ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Flock", "tray.log");
        writer = Task.Run(WriteAsync);
    }

    public bool IsUnavailable => Volatile.Read(ref unavailable) != 0;

    public long OverflowCount => Volatile.Read(ref overflow);

    public bool TryWrite(string record)
    {
        if (Volatile.Read(ref disposed) != 0)
        {
            Interlocked.Increment(ref overflow);
            return false;
        }
        var totalOverflow = Volatile.Read(ref overflow);
        var unreportedOverflow = totalOverflow - Volatile.Read(ref reportedOverflow);
        var payload = unreportedOverflow > 0
            ? $"[log sink dropped {unreportedOverflow} record(s)] {record}"
            : record;
        lock (drainGate)
        {
            if (pending++ == 0)
            {
                drained = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            }
        }
        if (IsUnavailable || !records.Writer.TryWrite(payload))
        {
            lock (drainGate)
            {
                if (--pending == 0)
                {
                    drained.TrySetResult();
                }
            }
            Interlocked.Increment(ref overflow);
            return false;
        }
        if (unreportedOverflow > 0)
        {
            Interlocked.Exchange(ref reportedOverflow, totalOverflow);
        }
        return true;
    }

    public Task DrainAsync()
    {
        lock (drainGate)
        {
            return drained.Task;
        }
    }

    private async Task WriteAsync()
    {
        try
        {
            if (Path.GetDirectoryName(path) is { Length: > 0 } directory)
            {
                Directory.CreateDirectory(directory);
            }
            await using var file = new StreamWriter(new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite), System.Text.Encoding.UTF8);
            await foreach (var record in records.Reader.ReadAllAsync(stop.Token))
            {
                await file.WriteLineAsync(record);
                await file.FlushAsync();
                lock (drainGate)
                {
                    if (--pending == 0)
                    {
                        drained.TrySetResult();
                    }
                }
            }
        }
        catch (OperationCanceledException) when (stop.IsCancellationRequested)
        {
        }
        catch (IOException)
        {
            Interlocked.Exchange(ref unavailable, 1);
            CompletePending();
        }
        catch (UnauthorizedAccessException)
        {
            Interlocked.Exchange(ref unavailable, 1);
            CompletePending();
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0)
        {
            return;
        }
        records.Writer.TryComplete();
        await writer;
        stop.Dispose();
    }

    /// <summary>Writes a startup failure when no application host exists to own the normal sink.</summary>
    public static void WriteEmergency(Exception error)
    {
        try
        {
            var sink = new FileLogSink();
            sink.TryWrite($"{DateTimeOffset.Now:O} fatal: {error}");
            sink.DrainAsync().GetAwaiter().GetResult();
            sink.DisposeAsync().AsTask().GetAwaiter().GetResult();
        }
        catch (Exception)
        {
            // Native startup reporting remains the fallback when the diagnostic path is unavailable.
        }
    }

    private static TaskCompletionSource CompletedDrain()
    {
        var result = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        result.SetResult();
        return result;
    }

    private void CompletePending()
    {
        lock (drainGate)
        {
            pending = 0;
            drained.TrySetResult();
        }
    }
}