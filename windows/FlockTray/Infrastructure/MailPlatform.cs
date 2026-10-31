using Windows.Storage.Pickers;

namespace Flock.Tray.Infrastructure;

/// <summary>Scoped native boundary for mail attachment picking and writing.</summary>
public interface IMailPlatform
{
    Task<string?> PickSaveFileAsync(IntPtr owner, string fileName, string extension, CancellationToken cancellationToken = default);
    Task WriteFileAsync(string path, ReadOnlyMemory<byte> data, CancellationToken cancellationToken = default);
}

public sealed class MailPlatform : IMailPlatform
{
    public async Task<string?> PickSaveFileAsync(IntPtr owner, string fileName, string extension, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var picker = new FileSavePicker { SuggestedFileName = fileName.Length == 0 ? "attachment" : fileName };
        var normalized = extension.Length == 0 ? ".bin" : extension.StartsWith('.') ? extension : "." + extension;
        picker.FileTypeChoices.Add(normalized == ".bin" ? "File" : normalized.TrimStart('.').ToUpperInvariant() + " file", [normalized]);
        WinRT.Interop.InitializeWithWindow.Initialize(picker, owner);
        var file = await picker.PickSaveFileAsync();
        cancellationToken.ThrowIfCancellationRequested();
        return file?.Path;
    }

    public async Task WriteFileAsync(string path, ReadOnlyMemory<byte> data, CancellationToken cancellationToken = default)
    {
        await using var stream = new FileStream(path, FileMode.Create, FileAccess.Write, FileShare.None, 64 * 1024,
            FileOptions.Asynchronous | FileOptions.SequentialScan);
        await stream.WriteAsync(data, cancellationToken);
        await stream.FlushAsync(cancellationToken);
    }
}