using System.Net;
using System.Security.Cryptography;
using Flock.Kit;
using Flock.Tray.Features.Update;
using Flock.Tray.Infrastructure;
using Microsoft.Extensions.Logging.Abstractions;

namespace Flock.Tray.Tests;

public sealed class UpdateServiceTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-update-").FullName;
    private readonly UpdateHandler handler = new();
    private readonly FakeMetadata metadata;
    private readonly FakeVerifier verifier = new();
    private readonly FakeLauncher launcher = new();
    private readonly FakeCleanup cleanup;
    private readonly FakeShutdown shutdown = new();
    private readonly UpdateService service;
    private readonly HttpClient http;

    public UpdateServiceTests()
    {
        metadata = new FakeMetadata(directory, AppVersion.Parse("1.0.0")!);
        cleanup = new FakeCleanup();
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        service = new UpdateService(http, TimeProvider.System, metadata, launcher, verifier, cleanup,
            new OperationRunner(), shutdown, NullLogger<UpdateService>.Instance, () => metadata.Channel);
    }

    public void Dispose()
    {
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task UnversionedBuildRejectsAllUpdateEntryPoints()
    {
        metadata.Current = null;

        Assert.False(service.IsEnabled);
        Assert.Equal(OperationOutcome.Rejected, await service.CheckAsync(userInitiated: true));
        Assert.Equal(OperationOutcome.Rejected, await service.InstallAsync());
        Assert.Equal(OperationOutcome.Rejected, await service.CheckAndInstallAsync());
        Assert.Equal(0, handler.FeedRequests);
        Assert.False(launcher.Called);
    }

    [Fact]
    public async Task ConcurrentChecksJoinOneReadAndKeepTheLatestOwner()
    {
        handler.Feed = Feed("1.1.0", "stable", 1);
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        handler.BeforeResponse = async () =>
        {
            entered.SetResult();
            await release.Task;
        };

        var first = service.CheckAsync(userInitiated: true);
        await entered.Task;
        var second = service.CheckAsync(userInitiated: false);
        release.SetResult();

        Assert.Equal(OperationOutcome.Succeeded, await first);
        Assert.Equal(OperationOutcome.Succeeded, await second);
        Assert.Equal(1, handler.FeedRequests);
        Assert.Equal("1.1.0", service.Available?.Version.ToString());
    }

    [Fact]
    public async Task CanceledDownloadCleansPartialAndDoesNotLaunch()
    {
        handler.Feed = Feed("1.1.0", "stable", 8);
        await service.CheckAsync(userInitiated: true);
        handler.Download = new BlockingStream([1, 2, 3]);
        using var cancellation = new CancellationTokenSource();
        var install = service.InstallAsync(cancellation.Token);
        await WaitForStateAsync(UpdatePhase.Downloading);
        cancellation.Cancel();

        Assert.Equal(OperationOutcome.Canceled, await install);
        Assert.False(launcher.Called);
        Assert.Single(cleanup.Paths, path => path.EndsWith(".part", StringComparison.Ordinal));
    }

    [Fact]
    public async Task MidstreamDownloadErrorCleansPartialAndDoesNotLaunch()
    {
        handler.Feed = Feed("1.1.0", "stable", 8);
        await service.CheckAsync(userInitiated: true);
        handler.Download = new BlockingStream([1, 2, 3], new IOException("connection lost"));

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());
        Assert.False(launcher.Called);
        Assert.Single(cleanup.Paths, path => path.EndsWith(".part", StringComparison.Ordinal));
    }

    [Fact]
    public async Task VerificationFailureCleansInstallerAndDoesNotLaunch()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        verifier.Error = new InvalidDataException("wrong publisher");

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());
        Assert.False(launcher.Called);
        Assert.Contains(cleanup.Paths, path => path.EndsWith(".exe", StringComparison.Ordinal));
        Assert.Contains("wrong publisher", service.ActionError);
    }

    [Fact]
    public async Task CleanupFailureDoesNotReplaceThePrimaryVerificationFailure()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        verifier.Error = new InvalidDataException("untrusted signer");
        cleanup.Fail = true;

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());
        Assert.Contains("untrusted signer", service.ActionError);
        Assert.NotEmpty(cleanup.Paths);
    }

    [Fact]
    public async Task LaunchFailureKeepsAppRunningAndCleansInstaller()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        launcher.Result = false;

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());
        Assert.False(shutdown.Requested);
        Assert.Contains(cleanup.Paths, path => path.EndsWith(".exe", StringComparison.Ordinal));
        Assert.Equal(UpdatePhase.Idle, service.State);
    }

    [Fact]
    public async Task SuccessfulLaunchTransfersFileAndRequestsQuitAfterOperationLeavesRegistry()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);

        var outcome = await service.InstallAsync();
        Assert.True(outcome == OperationOutcome.Succeeded, service.ActionError ?? "no update error");
        Assert.True(launcher.Called);
        Assert.True(shutdown.Requested);
        Assert.Empty(cleanup.Paths);
        Assert.False(service.IsBusy);
        Assert.Equal(["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/RELAUNCH"], launcher.Arguments.Take(4));
        Assert.StartsWith("/RESULT=", launcher.Arguments[4]);
    }

    [Fact]
    public async Task DevelopmentBuildRejectsWithoutReadingTheFeed()
    {
        metadata.Current = null;

        Assert.Equal(OperationOutcome.Rejected, await service.CheckAsync(userInitiated: true));
        Assert.Equal(0, handler.FeedRequests);
        Assert.Null(service.ActionError);
    }

    [Fact]
    public async Task InstallRereadsTheFeedAndUsesTheNewRelease()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        handler.Feed = Feed("1.2.0", "stable", 3);

        Assert.Equal(OperationOutcome.Succeeded, await service.InstallAsync());

        Assert.Equal(2, handler.FeedRequests);
        Assert.Contains("1.2.0", launcher.FileName);
    }

    [Fact]
    public async Task FailedFeedRecheckDoesNotInstallTheStaleRelease()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        handler.Fail = true;

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());
        Assert.False(launcher.Called);
        Assert.False(shutdown.Requested);
    }

    [Fact]
    public async Task DeclinedInstallerKeepsAppRunningAndShowsItsResult()
    {
        handler.Feed = Feed("1.1.0", "stable", 3);
        await service.CheckAsync(userInitiated: true);
        launcher.ExitCode = 5;
        launcher.ResultMessage = "Administrator permission was declined.";

        Assert.Equal(OperationOutcome.Failed, await service.InstallAsync());

        Assert.False(shutdown.Requested);
        Assert.Equal(UpdatePhase.Idle, service.State);
        Assert.Equal(launcher.ResultMessage, service.ActionError);
        handler.Feed = Feed("1.2.0", "stable", 3);
        await service.CheckAsync(userInitiated: false);
        Assert.Equal(launcher.ResultMessage, service.ActionError);
    }

    [Fact]
    public async Task ChannelSelectionChangesTheChosenRelease()
    {
        handler.Feed = Feed(("1.1.0", "stable", 1), ("1.2.0", "beta", 1));
        Assert.Equal(2, UpdateFeed.Parse(handler.Feed).Count);

        await service.CheckAsync(userInitiated: true);
        Assert.Equal("1.1.0", service.Available?.Version.ToString());

        metadata.Channel = "beta";
        await service.CheckAsync(userInitiated: true);
        Assert.Equal("1.2.0", service.Available?.Version.ToString());
    }

    [Fact]
    public async Task BackgroundFailureDoesNotClearUserActionFailure()
    {
        handler.Fail = true;
        Assert.Equal(OperationOutcome.Failed, await service.CheckAsync(userInitiated: true));
        var action = service.ActionError;
        handler.Fail = false;
        await service.CheckAsync(userInitiated: false);

        Assert.Equal(action, service.ActionError);
    }

    [Theory]
    [InlineData(4, "", "no checksum")]
    [InlineData(4, "0000000000000000000000000000000000000000000000000000000000000000", "checksum")]
    public async Task FileVerificationRejectsMissingOrWrongChecksum(long length, string checksum, string expected)
    {
        var path = Path.Combine(directory, "installer.exe");
        await File.WriteAllBytesAsync(path, [1, 2, 3, 4]);
        var item = Item(length, checksum);
        var fileVerifier = new UpdateFileVerifier(new FakeAuthenticodeVerifier(0, "Guava s.r.o."));

        var error = await Assert.ThrowsAsync<InvalidDataException>(() => fileVerifier.VerifyAsync(path, item));

        Assert.Contains(expected, error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task FileVerificationRejectsUntrustedOrWrongPublisher()
    {
        var path = Path.Combine(directory, "installer.exe");
        var bytes = new byte[] { 1, 2, 3, 4 };
        await File.WriteAllBytesAsync(path, bytes);
        var item = Item(bytes.Length, Convert.ToHexString(SHA256.HashData(bytes)));

        var trustError = await Assert.ThrowsAsync<InvalidDataException>(() =>
            new UpdateFileVerifier(new FakeAuthenticodeVerifier(1, "Guava s.r.o.")).VerifyAsync(path, item));
        var publisherError = await Assert.ThrowsAsync<InvalidDataException>(() =>
            new UpdateFileVerifier(new FakeAuthenticodeVerifier(0, "Someone else")).VerifyAsync(path, item));

        Assert.Contains("trusted", trustError.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Someone else", publisherError.Message);
    }

    [Fact]
    public async Task FileVerificationRejectsWrongSize()
    {
        var path = Path.Combine(directory, "installer.exe");
        var bytes = new byte[] { 1, 2, 3, 4 };
        await File.WriteAllBytesAsync(path, bytes);
        var item = Item(bytes.Length + 1, Convert.ToHexString(SHA256.HashData(bytes)));

        var error = await Assert.ThrowsAsync<InvalidDataException>(() =>
            new UpdateFileVerifier(new FakeAuthenticodeVerifier(0, "Guava s.r.o.")).VerifyAsync(path, item));

        Assert.Contains("instead of", error.Message);
    }

    private async Task WaitForStateAsync(UpdatePhase expected)
    {
        for (var attempt = 0; attempt < 100 && service.State != expected; attempt++)
        {
            await Task.Yield();
        }
        Assert.Equal(expected, service.State);
    }

    private static UpdateFeed.Item Item(long length, string checksum, string channel = "stable") => new(
        AppVersion.Parse("1.1.0")!, channel, "windows", "https://example.test/Flock.exe", length, checksum,
        new Version(10, 0, 19041), "", null);

    private static string Feed(string version, string channel, int length) => Feed((version, channel, length));

    private static string Feed(params (string Version, string Channel, int Length)[] releases) => $"""
        <rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
          <channel>{string.Join("", releases.Select(release => $"<item><sparkle:shortVersionString>{release.Version}</sparkle:shortVersionString><sparkle:channel>{(release.Channel == "stable" ? "" : release.Channel)}</sparkle:channel><enclosure url=\"https://example.test/Flock-{release.Version}.exe\" length=\"{release.Length}\" sparkle:os=\"windows\" /></item>"))}</channel>
        </rss>
        """;

    private sealed class FakeMetadata(string directory, AppVersion? current) : IUpdateInstallationMetadata
    {
        public AppVersion? Current { get; set; } = current;
        public string Channel { get; set; } = "stable";
        public AppVersion? CurrentVersion => Current;
        public Version OperatingSystemVersion => new(10, 0, 26100);
        public string DownloadDirectory => directory;
    }

    private sealed class FakeVerifier : IUpdateFileVerifier
    {
        public Exception? Error { get; set; }

        public Task VerifyAsync(string path, UpdateFeed.Item update, CancellationToken cancellationToken = default) =>
            Error is null ? Task.CompletedTask : Task.FromException(Error);
    }

    private sealed class FakeLauncher : IUpdateProcessLauncher
    {
        public bool Result { get; set; } = true;
        public bool Called { get; private set; }
        public IReadOnlyList<string> Arguments { get; private set; } = [];
        public string FileName { get; private set; } = "";
        public int ExitCode { get; set; }
        public string? ResultMessage { get; set; }

        public Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments)
        {
            Called = true;
            FileName = fileName;
            Arguments = [.. arguments];
            if (ResultMessage is not null)
            {
                File.WriteAllText(arguments.Single(argument => argument.StartsWith("/RESULT=", StringComparison.Ordinal))[8..], ResultMessage);
            }
            return Task.FromResult(new CliProcessResult(Result, Result ? ExitCode : -1, ""));
        }
    }

    private sealed class FakeCleanup : IUpdateFileCleanup
    {
        public List<string> Paths { get; } = [];
        public bool Fail { get; set; }

        public Task DeleteAsync(string path, CancellationToken cancellationToken = default)
        {
            Paths.Add(path);
            if (Fail)
            {
                throw new IOException("cleanup failed");
            }
            File.Delete(path);
            return Task.CompletedTask;
        }
    }

    private sealed class FakeShutdown : IShutdownRequest
    {
        public bool Requested { get; private set; }
        public void RequestQuit() => Requested = true;
    }

    private sealed class FakeAuthenticodeVerifier(int trustCode, string publisher) : IAuthenticodeVerifier
    {
        public AuthenticodeVerification Verify(string path) => new(trustCode, publisher);
    }

    private sealed class UpdateHandler : HttpMessageHandler
    {
        public string Feed { get; set; } = "";
        public Stream? Download { get; set; }
        public bool Fail { get; set; }
        public int FeedRequests { get; private set; }
        public Func<Task>? BeforeResponse { get; set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (BeforeResponse is not null)
            {
                await BeforeResponse();
            }
            if (request.RequestUri?.ToString() == UpdateFeed.Url)
            {
                FeedRequests++;
                if (Fail)
                {
                    throw new HttpRequestException("offline");
                }
                return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(Feed) };
            }
            HttpContent content = Download is null ? new ByteArrayContent([1, 2, 3]) : new StreamContent(Download);
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = content };
        }
    }

    private sealed class BlockingStream(byte[] first, Exception? error = null) : Stream
    {
        private bool returned;

        public override async ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
        {
            if (!returned)
            {
                first.AsSpan().CopyTo(buffer.Span);
                returned = true;
                return first.Length;
            }
            if (error is not null)
            {
                throw error;
            }
            await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            return 0;
        }

        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => first.Length;
        public override long Position { get; set; }
        public override void Flush() { }
        public override int Read(byte[] buffer, int offset, int count) => throw new NotSupportedException();
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }
}
