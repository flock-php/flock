using System.Reflection;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.Extensions.Logging;

namespace Flock.Tray.Features.Update;

public interface IUpdateInstallationMetadata
{
    AppVersion? CurrentVersion { get; }
    Version OperatingSystemVersion { get; }
    string DownloadDirectory { get; }
}

public sealed class UpdateInstallationMetadata : IUpdateInstallationMetadata
{
    public AppVersion? CurrentVersion => UpdateService.Current;
    public Version OperatingSystemVersion => Environment.OSVersion.Version;
    public string DownloadDirectory => Path.Combine(Path.GetTempPath(), "Flock");
}

public interface IUpdateProcessLauncher
{
    Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments);
}

public sealed class UpdateProcessLauncher : IUpdateProcessLauncher
{
    public async Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments)
    {
        var start = new ProcessStartInfo(fileName) { UseShellExecute = true };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }
        using var process = Process.Start(start);
        if (process is null)
        {
            return new CliProcessResult(false, -1, "The verified installer could not be started.");
        }
        // Setup kills this app only after its privileged preflight succeeds. A declined UAC prompt
        // returns here with the existing app still running. Do not cancel or kill the installer.
        await process.WaitForExitAsync();
        return new CliProcessResult(true, process.ExitCode, "");
    }
}

public interface IUpdateFileCleanup
{
    Task DeleteAsync(string path, CancellationToken cancellationToken = default);
}

public sealed class UpdateFileCleanup : IUpdateFileCleanup
{
    public Task DeleteAsync(string path, CancellationToken cancellationToken = default) => Task.Run(() => File.Delete(path), cancellationToken);
}

public interface IAuthenticodeVerifier
{
    AuthenticodeVerification Verify(string path);
}

public sealed record AuthenticodeVerification(int TrustCode, string? Publisher);

public sealed class WindowsAuthenticodeVerifier : IAuthenticodeVerifier
{
    private const uint CertQueryObjectFile = 1;
    private const uint CertQueryContentFlagPkcs7SignedEmbed = 0x00000400;
    private const uint CertQueryFormatFlagBinary = 2;
    private const uint CmsgSignerCertInfoParam = 7;
    private const uint CertFindSubjectCert = 0x000B0000;

    public AuthenticodeVerification Verify(string path)
    {
        var trustCode = Native.VerifySignature(path);
        return new AuthenticodeVerification(trustCode, trustCode == 0 ? ReadPublisher(path) : null);
    }

    internal static string ReadPublisher(string path)
    {
        if (!CryptQueryObject(CertQueryObjectFile, path, CertQueryContentFlagPkcs7SignedEmbed,
                CertQueryFormatFlagBinary, 0, out var encoding, out _, out _, out var store, out var message, out _))
        {
            throw new CryptographicException(Marshal.GetLastWin32Error());
        }

        var context = IntPtr.Zero;
        var signerInfo = IntPtr.Zero;
        try
        {
            // Identify the signer by issuer and serial number, never by certificate-store order.
            uint size = 0;
            if (!CryptMsgGetParam(message, CmsgSignerCertInfoParam, 0, IntPtr.Zero, ref size))
            {
                throw new CryptographicException(Marshal.GetLastWin32Error());
            }
            signerInfo = Marshal.AllocHGlobal(checked((int)size));
            if (!CryptMsgGetParam(message, CmsgSignerCertInfoParam, 0, signerInfo, ref size))
            {
                throw new CryptographicException(Marshal.GetLastWin32Error());
            }
            context = CertFindCertificateInStore(store, encoding, 0, CertFindSubjectCert, signerInfo, IntPtr.Zero);
            if (context == IntPtr.Zero)
            {
                throw new CryptographicException(Marshal.GetLastWin32Error());
            }
            var certificate = Marshal.PtrToStructure<CERT_CONTEXT>(context);
            var bytes = new byte[certificate.cbCertEncoded];
            Marshal.Copy(certificate.pbCertEncoded, bytes, 0, bytes.Length);
            using var signer = X509CertificateLoader.LoadCertificate(bytes);
            return new X500DistinguishedName(signer.Subject).EnumerateRelativeDistinguishedNames()
                .FirstOrDefault(name => !name.HasMultipleElements && name.GetSingleElementType().Value == "2.5.4.10")
                ?.GetSingleElementValue() ?? signer.Subject;
        }
        finally
        {
            if (context != IntPtr.Zero)
            {
                CertFreeCertificateContext(context);
            }
            Marshal.FreeHGlobal(signerInfo);
            CryptMsgClose(message);
            CertCloseStore(store, 0);
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct CERT_CONTEXT
    {
        public uint dwCertEncodingType;
        public IntPtr pbCertEncoded;
        public uint cbCertEncoded;
        public IntPtr pCertInfo;
        public IntPtr hCertStore;
    }

    [DllImport("Crypt32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool CryptQueryObject(
        uint dwObjectType,
        string pvObject,
        uint dwExpectedContentTypeFlags,
        uint dwExpectedFormatTypeFlags,
        uint dwFlags,
        out uint pdwMsgAndCertEncodingType,
        out uint pdwContentType,
        out uint pdwFormatType,
        out IntPtr phCertStore,
        out IntPtr phMsg,
        out IntPtr ppvContext);

    [DllImport("Crypt32.dll", SetLastError = true)]
    private static extern bool CertFreeCertificateContext(IntPtr certificateContext);

    [DllImport("Crypt32.dll", SetLastError = true)]
    private static extern bool CryptMsgGetParam(IntPtr message, uint parameter, uint index, IntPtr data, ref uint size);

    [DllImport("Crypt32.dll", SetLastError = true)]
    private static extern IntPtr CertFindCertificateInStore(IntPtr certificateStore, uint encoding, uint flags,
        uint findType, IntPtr findParameter, IntPtr previousCertificateContext);

    [DllImport("Crypt32.dll", SetLastError = true)]
    private static extern bool CryptMsgClose(IntPtr message);

    [DllImport("Crypt32.dll", SetLastError = true)]
    private static extern bool CertCloseStore(IntPtr certificateStore, uint flags);
}

public interface IUpdateFileVerifier
{
    Task VerifyAsync(string path, UpdateFeed.Item update, CancellationToken cancellationToken = default);
}

public sealed class UpdateFileVerifier(IAuthenticodeVerifier authenticode) : IUpdateFileVerifier
{
    private const string Publisher = "Guava s.r.o.";

    public async Task VerifyAsync(string path, UpdateFeed.Item update, CancellationToken cancellationToken = default)
    {
        var length = new FileInfo(path).Length;
        if (update.Length <= 0 || length != update.Length)
        {
            throw new InvalidDataException($"The download has {length} bytes instead of {update.Length}.");
        }
        if (string.IsNullOrWhiteSpace(update.Sha256))
        {
            throw new InvalidDataException("The update feed lists no checksum for it.");
        }

        await using (var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read,
            81920, FileOptions.Asynchronous | FileOptions.SequentialScan))
        {
            var hash = Convert.ToHexString(await SHA256.HashDataAsync(stream, cancellationToken));
            if (!hash.Equals(update.Sha256, StringComparison.OrdinalIgnoreCase))
            {
                throw new InvalidDataException("The download doesn't match its checksum.");
            }
        }

        var signature = await Task.Run(() => authenticode.Verify(path), cancellationToken);
        if (signature.TrustCode != 0)
        {
            throw new InvalidDataException($"Its signature isn't trusted (0x{signature.TrustCode:X8}).");
        }
        if (!string.Equals(signature.Publisher, Publisher, StringComparison.Ordinal))
        {
            throw new InvalidDataException($"It is signed by {signature.Publisher ?? "an unknown publisher"}, not {Publisher}.");
        }
    }
}

public enum UpdatePhase { Idle, Checking, Downloading, Installing }

/// <summary>Checks, verifies, and hands off app updates without owning any UI or host lifetime.</summary>
public sealed class UpdateService
{
    private const string OperationKey = "updates";
    private static readonly TimeSpan CheckDeadline = TimeSpan.FromSeconds(30);
    private static readonly TimeSpan DownloadDeadline = TimeSpan.FromMinutes(15);

    public static AppVersion? Current { get; } = AppVersion.FromInformational(
        Assembly.GetExecutingAssembly().GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion);

    private readonly HttpClient http;
    private readonly TimeProvider timeProvider;
    private readonly IUpdateInstallationMetadata installation;
    private readonly IUpdateProcessLauncher processLauncher;
    private readonly IUpdateFileVerifier verifier;
    private readonly IUpdateFileCleanup cleanup;
    private readonly OperationRunner operations;
    private readonly IShutdownRequest shutdown;
    private readonly ILogger<UpdateService> logger;
    private readonly Func<string?> channel;
    private readonly IUiDispatcher? dispatcher;
    private readonly OperationErrors errors = new();
    private readonly object checkGate = new();
    private Task<CheckExecution>? activeCheck;
    private bool ownershipTransferred;

    public UpdateService(
        HttpClient http,
        TimeProvider timeProvider,
        IUpdateInstallationMetadata installation,
        IUpdateProcessLauncher processLauncher,
        IUpdateFileVerifier verifier,
        IUpdateFileCleanup cleanup,
        OperationRunner operations,
        IShutdownRequest shutdown,
        ILogger<UpdateService> logger,
        Func<string?> channel,
        IUiDispatcher? dispatcher = null)
    {
        this.http = http;
        this.timeProvider = timeProvider;
        this.installation = installation;
        this.processLauncher = processLauncher;
        this.verifier = verifier;
        this.cleanup = cleanup;
        this.operations = operations;
        this.shutdown = shutdown;
        this.logger = logger;
        this.channel = channel;
        this.dispatcher = dispatcher;
    }

    public bool IsEnabled => installation.CurrentVersion is not null;
    public UpdateFeed.Item? Available { get; private set; }
    public UpdatePhase State { get; private set; }
    public int Progress { get; private set; }
    public bool IsUpToDate { get; private set; }
    public bool IsBusy => operations.IsRunning(OperationKey);
    public string? ActionError => errors.Action;
    public string? RefreshError => errors.Refresh;
    public string? Error => errors.DisplayedMessage;
    public string Summary => State switch
    {
        UpdatePhase.Checking => "Checking…",
        UpdatePhase.Downloading => $"Downloading… {Progress}%",
        UpdatePhase.Installing => "Installing…",
        _ when Error is not null => Error,
        _ when Available is { } update => $"{update.Version} is available",
        _ when IsUpToDate => "Up to date",
        _ => "",
    };

    public event Action? Changed;

    public Task<OperationOutcome> CheckAsync(bool userInitiated, CancellationToken cancellationToken = default)
    {
        if (!IsEnabled)
        {
            return Task.FromResult(OperationOutcome.Rejected);
        }

        Task<CheckExecution> check;
        lock (checkGate)
        {
            if (activeCheck is null or { IsCompleted: true })
            {
                activeCheck = RunCheckAsync(cancellationToken);
            }
            check = activeCheck;
        }
        return ObserveCheckAsync(check, userInitiated, cancellationToken);
    }

    public Task<OperationOutcome> CheckAndInstallAsync(CancellationToken cancellationToken = default) =>
        IsEnabled ? RunOperationAsync(CheckAndInstallCoreAsync, cancellationToken, action: true)
            : Task.FromResult(OperationOutcome.Rejected);

    public Task<OperationOutcome> InstallAsync(CancellationToken cancellationToken = default)
    {
        if (Available is null || !IsEnabled)
        {
            return Task.FromResult(OperationOutcome.Rejected);
        }
        return RunOperationAsync(CheckAndInstallCoreAsync, cancellationToken, action: true);
    }

    public void DismissError()
    {
        errors.ClearAction();
        errors.ClearRefresh();
        Changed?.Invoke();
    }

    private async Task<CheckExecution> RunCheckAsync(CancellationToken cancellationToken)
    {
        Exception? failure = null;
        var outcome = await RunOperationAsync(async token =>
        {
            try
            {
                await SetStateAsync(UpdatePhase.Checking, 0);
                using var deadline = new RequestDeadline(timeProvider, token, CheckDeadline);
                var items = UpdateFeed.Parse(await http.GetStringAsync(UpdateFeed.Url, deadline.Token));
                Available = UpdateFeed.Pick(items, installation.CurrentVersion, UpdateFeed.TakesBetas(channel()), installation.OperatingSystemVersion);
                IsUpToDate = Available is null;
            }
            catch (Exception error)
            {
                failure = error;
                throw;
            }
            finally
            {
                await SetStateAsync(UpdatePhase.Idle);
            }
        }, cancellationToken, action: false, reportFailure: false);
        if (outcome == OperationOutcome.Failed && failure is not null)
        {
            logger.LogError(failure, "Update operation {Operation} failed for {Resource}", "check", "installer");
        }
        return new CheckExecution(outcome, failure);
    }

    private async Task<OperationOutcome> ObserveCheckAsync(Task<CheckExecution> check, bool userInitiated, CancellationToken cancellationToken)
    {
        CheckExecution result;
        try
        {
            result = await check.WaitAsync(cancellationToken);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return OperationOutcome.Canceled;
        }

        if (result.Outcome == OperationOutcome.Failed && result.Error is not null)
        {
            if (userInitiated)
            {
                SetError(action: true, result.Error);
            }
            else
            {
                SetError(action: false, result.Error);
            }
            await PublishAsync();
        }
        else if (result.Outcome == OperationOutcome.Succeeded)
        {
            if (userInitiated)
            {
                errors.ClearAction();
            }
            else
            {
                errors.ClearRefresh();
            }
            await PublishAsync();
        }
        return result.Outcome;
    }

    private async Task CheckAndInstallCoreAsync(CancellationToken cancellationToken)
    {
        await SetStateAsync(UpdatePhase.Checking, 0);
        using var deadline = new RequestDeadline(timeProvider, cancellationToken, CheckDeadline);
        var items = UpdateFeed.Parse(await http.GetStringAsync(UpdateFeed.Url, deadline.Token));
        Available = UpdateFeed.Pick(items, installation.CurrentVersion, UpdateFeed.TakesBetas(channel()), installation.OperatingSystemVersion);
        IsUpToDate = Available is null;
        await SetStateAsync(UpdatePhase.Idle);
        if (Available is not null)
        {
            await InstallCoreAsync(cancellationToken);
        }
    }

    private async Task InstallCoreAsync(CancellationToken cancellationToken)
    {
        if (Available is not { } update)
        {
            throw new InvalidOperationException("No update is available.");
        }

        ownershipTransferred = false;
        await SetStateAsync(UpdatePhase.Downloading, 0);
        var files = new DownloadFiles(installation.DownloadDirectory, update.Version.ToString());
        var transferred = false;
        try
        {
            await DownloadAsync(files, update, cancellationToken);
            await verifier.VerifyAsync(files.Installer, update, cancellationToken);
            await SetStateAsync(UpdatePhase.Installing);
            var resultPath = files.Installer + ".result.txt";
            File.Delete(resultPath);
            var result = await processLauncher.RunAsync(files.Installer,
                ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/RELAUNCH", $"/RESULT={resultPath}"]);
            if (!result.Succeeded)
            {
                var message = File.Exists(resultPath) ? (await File.ReadAllTextAsync(resultPath)).Trim() : result.Output;
                throw new InvalidOperationException(message.Length > 0 ? message : $"The update was not installed (setup exited with code {result.ExitCode}).");
            }
            transferred = true;
            ownershipTransferred = true;
        }
        finally
        {
            if (!transferred)
            {
                await CleanupOwnedAsync(files);
            }
        }
    }

    private async Task DownloadAsync(DownloadFiles files, UpdateFeed.Item update, CancellationToken cancellationToken)
    {
        try
        {
            Directory.CreateDirectory(files.Directory);
            using var deadline = new RequestDeadline(timeProvider, cancellationToken, DownloadDeadline);
            using var response = await http.GetAsync(update.Url, HttpCompletionOption.ResponseHeadersRead, deadline.Token);
            response.EnsureSuccessStatusCode();
            var total = response.Content.Headers.ContentLength ?? update.Length;
            await using (var source = await response.Content.ReadAsStreamAsync(deadline.Token))
            await using (var target = new FileStream(files.Partial, FileMode.Create, FileAccess.Write, FileShare.None,
                81920, FileOptions.Asynchronous | FileOptions.SequentialScan))
            {
                files.PartialOwned = true;
                var buffer = new byte[81920];
                long completed = 0;
                int read;
                while ((read = await source.ReadAsync(buffer.AsMemory(), deadline.Token)) > 0)
                {
                    await target.WriteAsync(buffer.AsMemory(0, read), deadline.Token);
                    completed += read;
                    var progress = total > 0 ? (int)Math.Min(100, completed * 100 / total) : 0;
                    if (progress != Progress)
                    {
                        await SetStateAsync(UpdatePhase.Downloading, progress);
                    }
                }
                await target.FlushAsync(deadline.Token);
            }
            File.Move(files.Partial, files.Installer, overwrite: true);
            files.PartialOwned = false;
            files.InstallerOwned = true;
        }
        catch
        {
            await CleanupOwnedAsync(files);
            throw;
        }
    }

    private async Task<OperationOutcome> RunOperationAsync(Func<CancellationToken, Task> operation, CancellationToken cancellationToken,
        bool action, bool reportFailure = true)
    {
        Exception? failure = null;
        var internalErrors = new OperationErrors();
        var outcome = await operations.RunMutationAsync(OperationKey, internalErrors, async token =>
        {
            try
            {
                await operation(token);
            }
            catch (Exception error)
            {
                failure = error;
                throw;
            }
        }, cancellationToken);

        var transferred = ownershipTransferred;
        ownershipTransferred = false;
        if (outcome == OperationOutcome.Succeeded && transferred)
        {
            shutdown.RequestQuit();
        }
        else if (outcome == OperationOutcome.Succeeded && action)
        {
            errors.ClearAction();
            await PublishAsync();
        }
        else if (action && outcome is (OperationOutcome.Failed or OperationOutcome.Canceled))
        {
            await SetStateAsync(UpdatePhase.Idle);
        }

        if (reportFailure && outcome == OperationOutcome.Failed && failure is not null)
        {
            logger.LogError(failure, "Update operation {Operation} failed for {Resource}", action ? "install" : "check", "installer");
            if (action)
            {
                SetError(action: true, failure);
            }
            else
            {
                SetError(action: false, failure);
            }
            await PublishAsync();
        }
        return outcome;
    }

    private void SetError(bool action, Exception error)
    {
        var message = error switch
        {
            InvalidDataException => error.Message,
            OperationCanceledException => "The update request took too long.",
            HttpRequestException => "Could not reach the update server.",
            UnauthorizedAccessException => "Flock could not access the update file.",
            _ => error.Message.Length > 0 ? error.Message : "The update could not be completed.",
        };
        if (action)
        {
            errors.SetAction(new InvalidOperationException(message));
        }
        else
        {
            errors.SetRefresh(new InvalidOperationException(message));
        }
    }

    private async Task CleanupOwnedAsync(DownloadFiles files)
    {
        foreach (var (path, owned, markReleased) in new[]
        {
            (files.Partial, files.PartialOwned, (Action)(() => files.PartialOwned = false)),
            (files.Installer, files.InstallerOwned, (Action)(() => files.InstallerOwned = false)),
        })
        {
            if (!owned)
            {
                continue;
            }
            try
            {
                await cleanup.DeleteAsync(path);
                markReleased();
            }
            catch (Exception error)
            {
                logger.LogError(error, "Could not clean up an owned update file for {Resource}", "installer");
            }
        }
    }

    private async Task SetStateAsync(UpdatePhase state, int? progress = null)
    {
        State = state;
        if (progress is { } value)
        {
            Progress = value;
        }
        await PublishAsync();
    }

    private async Task PublishAsync()
    {
        if (dispatcher is not null)
        {
            await dispatcher.InvokeAsync(() => Changed?.Invoke(), CancellationToken.None);
        }
        else
        {
            Changed?.Invoke();
        }
    }

    private sealed record CheckExecution(OperationOutcome Outcome, Exception? Error);

    private sealed class DownloadFiles
    {
        public DownloadFiles(string directory, string version)
        {
            Directory = directory;
            Installer = Path.Combine(directory, $"Flock-Setup-{version}-x64.exe");
            Partial = Installer + ".part";
        }

        public string Directory { get; }
        public string Installer { get; }
        public string Partial { get; }
        public bool PartialOwned { get; set; }
        public bool InstallerOwned { get; set; }
    }

    private sealed class RequestDeadline : IDisposable
    {
        private readonly CancellationTokenSource cancellation;
        private readonly ITimer timer;

        public RequestDeadline(TimeProvider timeProvider, CancellationToken parent, TimeSpan deadline)
        {
            cancellation = CancellationTokenSource.CreateLinkedTokenSource(parent);
            timer = timeProvider.CreateTimer(static state =>
            {
                try
                {
                    ((CancellationTokenSource)state!).Cancel();
                }
                catch (ObjectDisposedException)
                {
                }
            }, cancellation,
                deadline, Timeout.InfiniteTimeSpan);
        }

        public CancellationToken Token => cancellation.Token;

        public void Dispose()
        {
            timer.Dispose();
            cancellation.Dispose();
        }
    }
}
