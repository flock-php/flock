namespace Flock.Tray.Infrastructure;

/// <summary>Owns one HTML view's temporary page directory and never cleans another view's files.</summary>
public sealed class HtmlPageFiles : IAsyncDisposable
{
    private readonly string directory;
    private readonly Action<Exception>? log;
    private readonly SemaphoreSlim gate = new(1, 1);
    private readonly HashSet<string> files = new(StringComparer.OrdinalIgnoreCase);
    private readonly object stateGate = new();
    private Task? releaseTask;
    private bool released;

    public HtmlPageFiles(string? root = null, Action<Exception>? log = null)
    {
        var parent = root ?? Path.Combine(Path.GetTempPath(), "Flock");
        directory = Path.Combine(parent, "html-" + Guid.NewGuid().ToString("N"));
        this.log = log;
    }

    public string DirectoryPath => directory;

    public Task<string> WriteAsync(string html, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(html);
        return WriteCoreAsync(html, cancellationToken);
    }

    public Task ReleasePageAsync(string path)
    {
        ArgumentNullException.ThrowIfNull(path);
        return ReleasePageCoreAsync(path);
    }

    public Task ReleaseAsync()
    {
        lock (stateGate)
        {
            return releaseTask ??= ReleaseCoreAsync();
        }
    }

    public ValueTask DisposeAsync() => new(ReleaseAsync());

    private async Task<string> WriteCoreAsync(string html, CancellationToken cancellationToken)
    {
        await gate.WaitAsync(cancellationToken);
        string? path = null;
        try
        {
            lock (stateGate)
            {
                if (released)
                {
                    throw new ObjectDisposedException(nameof(HtmlPageFiles));
                }
                path = Path.Combine(directory, "page-" + Guid.NewGuid().ToString("N") + ".html");
                files.Add(path);
            }
            await Task.Run(() => WriteFileAsync(path, html, cancellationToken), cancellationToken);
            return path;
        }
        catch
        {
            if (path is not null)
            {
                lock (stateGate)
                {
                    files.Remove(path);
                }
                TryDelete(path);
            }
            throw;
        }
        finally
        {
            gate.Release();
        }
    }

    private static async Task WriteFileAsync(string path, string html, CancellationToken cancellationToken)
    {
        System.IO.Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        await using var stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.Read,
            64 * 1024, FileOptions.Asynchronous | FileOptions.SequentialScan);
        await using var writer = new StreamWriter(stream, new System.Text.UTF8Encoding(encoderShouldEmitUTF8Identifier: false), 64 * 1024, leaveOpen: true);
        await writer.WriteAsync(html.AsMemory(), cancellationToken);
        await writer.FlushAsync(cancellationToken);
        await stream.FlushAsync(cancellationToken);
    }

    private async Task ReleasePageCoreAsync(string path)
    {
        lock (stateGate)
        {
            if (released)
            {
                return;
            }
        }
        await gate.WaitAsync();
        try
        {
            lock (stateGate)
            {
                if (!files.Remove(path))
                {
                    return;
                }
            }
            TryDelete(path);
        }
        finally
        {
            gate.Release();
        }
    }

    private async Task ReleaseCoreAsync()
    {
        string[] owned;
        await gate.WaitAsync();
        try
        {
            lock (stateGate)
            {
                released = true;
                owned = [.. files];
                files.Clear();
            }
        }
        finally
        {
            gate.Release();
        }

        foreach (var path in owned)
        {
            TryDelete(path);
        }
        try
        {
            if (System.IO.Directory.Exists(directory))
            {
                System.IO.Directory.Delete(directory, recursive: false);
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            log?.Invoke(error);
        }
    }

    private void TryDelete(string path)
    {
        try
        {
            File.Delete(path);
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            log?.Invoke(error);
        }
    }
}