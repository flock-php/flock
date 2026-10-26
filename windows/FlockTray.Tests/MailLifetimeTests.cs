using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Mail;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class MailLifetimeTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-mail-").FullName;
    private readonly MailHandler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;

    public MailLifetimeTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
    }

    public void Dispose()
    {
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task ChangingMessageSuppressesTheOlderDetail()
    {
        handler.BlockOldDetail = true;
        await using var detail = new MailDetailViewModel(client, new FakeMailPlatform());
        var old = detail.SelectAsync("old");
        await handler.OldDetailStarted.Task;

        var current = detail.SelectAsync("current");
        await current;
        handler.ReleaseOldDetail.TrySetResult();
        await old;

        Assert.Equal("current", detail.Current?.Id);
        Assert.Null(detail.Error);
    }

    [Fact]
    public async Task RawFailureIsSeparateFromContentAndCanRetry()
    {
        await using var detail = new MailDetailViewModel(client, new FakeMailPlatform());
        await detail.SelectAsync("current");

        await detail.SetModeAsync("Raw");

        Assert.Null(detail.Raw);
        Assert.Equal("raw failed", detail.RawError);

        await detail.SetModeAsync("Raw");

        Assert.Equal("raw content", detail.Raw);
        Assert.Null(detail.RawError);
    }

    [Fact]
    public async Task SwitchingAwayFromRawSuppressesAStaleFailure()
    {
        handler.BlockRaw = true;
        await using var detail = new MailDetailViewModel(client, new FakeMailPlatform());
        await detail.SelectAsync("current");
        var raw = detail.SetModeAsync("Raw");
        await handler.RawStarted.Task;

        await detail.SetModeAsync("Text");
        handler.ReleaseRaw.TrySetResult();
        await raw;

        Assert.Equal("Text", detail.Mode);
        Assert.Null(detail.Raw);
        Assert.Null(detail.RawError);
    }

    [Fact]
    public async Task ClearDoesNotLetAnInFlightListRestoreOldMail()
    {
        handler.BlockFirstList = true;
        await using var service = new MailService(client, new OperationRunner());
        var refresh = service.RefreshAsync();
        await handler.FirstListStarted.Task;

        var clear = service.ClearAsync();
        await handler.ClearStarted.Task;
        handler.ReleaseFirstList.TrySetResult();
        await clear;
        await refresh;

        Assert.Empty(service.Messages);
    }

    [Fact]
    public async Task FailedClearKeepsTheExistingListAndActionError()
    {
        handler.FailClear = true;
        await using var service = new MailService(client, new OperationRunner());
        await service.RefreshAsync();

        var outcome = await service.ClearAsync();

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Single(service.Messages);
        Assert.Equal("clear failed", service.ActionError);
    }

    [Fact]
    public async Task AttachmentWriteFailureIsVisibleAndPickerCancelIsQuiet()
    {
        var platform = new FakeMailPlatform { Path = Path.Combine(directory, "attachment.bin"), FailWrite = true };
        await using var detail = new MailDetailViewModel(client, platform);
        await detail.SelectAsync("current");

        await detail.SaveAttachmentAsync(detail.Current!.Attachments[0], IntPtr.Zero);

        Assert.Equal("write failed", detail.ActionError);
        platform.Path = null;
        detail.DismissError();
        await detail.SaveAttachmentAsync(detail.Current.Attachments[0], IntPtr.Zero);
        Assert.Null(detail.ActionError);
    }

    [Fact]
    public async Task MailEnvironmentTracksPortChangesAndSurvivesAnUnavailableHint()
    {
        await using var service = new MailService(client, new OperationRunner());
        handler.Env = "MAIL_PORT=2525";
        await service.RefreshAsync();
        Assert.Equal(handler.Env, service.Env);
        handler.Env = "MAIL_PORT=2526";
        await service.RefreshAsync();
        Assert.Equal(handler.Env, service.Env);
        handler.FailServices = true;
        await service.RefreshAsync();
        Assert.Equal(handler.Env, service.Env);
        Assert.Null(service.Error);
    }

    private sealed class FakeMailPlatform : IMailPlatform
    {
        public string? Path { get; set; }
        public bool FailWrite { get; set; }

        public Task<string?> PickSaveFileAsync(IntPtr owner, string fileName, string extension, CancellationToken cancellationToken = default) =>
            Task.FromResult(Path);

        public Task WriteFileAsync(string path, ReadOnlyMemory<byte> data, CancellationToken cancellationToken = default)
        {
            if (FailWrite)
            {
                throw new IOException("write failed");
            }
            return File.WriteAllBytesAsync(path, data.ToArray(), cancellationToken);
        }
    }

    private sealed class MailHandler : HttpMessageHandler
    {
        public TaskCompletionSource OldDetailStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseOldDetail { get; } = NewSignal();
        public TaskCompletionSource RawStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseRaw { get; } = NewSignal();
        public TaskCompletionSource FirstListStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseFirstList { get; } = NewSignal();
        public TaskCompletionSource ClearStarted { get; } = NewSignal();
        public bool FailClear { get; set; }
        public bool BlockOldDetail { get; set; }
        public bool BlockRaw { get; set; }
        public bool BlockFirstList { get; set; }
        public string Env { get; set; } = "";
        public bool FailServices { get; set; }
        private int listReads;
        private int rawReads;

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            if (path == "/v1/services")
            {
                return FailServices ? Json("{}", HttpStatusCode.ServiceUnavailable)
                    : Json(FlockJson.Encode(new[] { new Service { Id = "mail", Env = Env } }));
            }
            if (path == "/v1/mail" && request.Method == HttpMethod.Get)
            {
                if (FailClear)
                {
                    return Json("[{\"id\":\"old\"}]");
                }
                if (Interlocked.Increment(ref listReads) == 1)
                {
                    FirstListStarted.TrySetResult();
                    if (BlockFirstList)
                    {
                        await ReleaseFirstList.Task;
                    }
                    return Json("[{\"id\":\"old\"}]");
                }
                return Json("[]");
            }
            if (path == "/v1/mail" && request.Method == HttpMethod.Delete)
            {
                ClearStarted.TrySetResult();
                return FailClear ? Json("{\"error\":\"clear failed\"}", HttpStatusCode.BadRequest) : Json("{}");
            }
            if (path == "/v1/mail/old")
            {
                OldDetailStarted.TrySetResult();
                if (BlockOldDetail)
                {
                    await ReleaseOldDetail.Task;
                }
                return Json("{\"id\":\"old\",\"subject\":\"old\"}");
            }
            if (path == "/v1/mail/current")
            {
                return Json("{\"id\":\"current\",\"subject\":\"current\",\"attachments\":[{\"index\":0,\"filename\":\"file.bin\"}]}");
            }
            if (path == "/v1/mail/current/raw")
            {
                if (Interlocked.Increment(ref rawReads) == 1)
                {
                    RawStarted.TrySetResult();
                    if (BlockRaw)
                    {
                        await ReleaseRaw.Task;
                    }
                    return Json("{\"error\":\"raw failed\"}", HttpStatusCode.BadRequest);
                }
                return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("raw content") };
            }
            if (path == "/v1/mail/current/attachments/0")
            {
                return new HttpResponseMessage(HttpStatusCode.OK) { Content = new ByteArrayContent([1, 2, 3]) };
            }
            return Json("{}");
        }

        private static TaskCompletionSource NewSignal() => new(TaskCreationOptions.RunContinuationsAsynchronously);

        private static HttpResponseMessage Json(string content, HttpStatusCode status = HttpStatusCode.OK) => new(status)
        {
            Content = new StringContent(content),
        };
    }
}
