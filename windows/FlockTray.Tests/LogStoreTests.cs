using System.Net;
using System.Text.Json;
using System.Threading.Channels;
using Microsoft.Extensions.Time.Testing;
using Flock.Kit;
using Flock.Tray;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class LogsViewModelTests : IAsyncLifetime
{
    private readonly string dir = Directory.CreateTempSubdirectory("flock-logs-").FullName;
    private readonly Handler handler = new();
    private readonly FakeTimeProvider requestClock = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly LogsViewModel viewModel;

    public LogsViewModelTests()
    {
        var path = Path.Combine(dir, "api.json");
        File.WriteAllText(path, """{"url":"http://127.0.0.1:1","token":"test"}""");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, path, requestClock);
        viewModel = new LogsViewModel(client, new OperationRunner());
    }

    public Task InitializeAsync() => Task.CompletedTask;

    public async Task DisposeAsync()
    {
        await viewModel.DisposeAsync();
        client.Dispose();
        http.Dispose();
        Directory.Delete(dir, recursive: true);
    }

    private sealed class Pending(HttpMethod method, string path)
    {
        public HttpMethod Method { get; } = method;
        public string Path { get; } = path;
        public TaskCompletionSource<HttpResponseMessage> Response { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public void Succeed(string body) => Response.SetResult(new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(body) });

        public void Fail(string message) => Response.SetResult(new HttpResponseMessage(HttpStatusCode.ServiceUnavailable)
        {
            Content = new StringContent(JsonSerializer.Serialize(new { error = message })),
        });

        public void Log(LogsViewModel.Source source, string text) =>
            Succeed(source is LogsViewModel.SiteSource ? JsonSerializer.Serialize(new[] { new LogEntry { Message = text } }, FlockJson.Options) : text);
    }

    private sealed class Handler : HttpMessageHandler
    {
        private readonly Channel<Pending> requests = Channel.CreateUnbounded<Pending>();

        public async Task<Pending> NextAsync()
        {
            // This is a deadlock watchdog, not the endpoint deadline or a timing assertion.
            using var watchdog = new CancellationTokenSource(TimeSpan.FromSeconds(60));
            return await requests.Reader.ReadAsync(watchdog.Token);
        }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var pending = new Pending(request.Method, request.RequestUri!.AbsolutePath);
            await requests.Writer.WriteAsync(pending, cancellationToken);
            return await pending.Response.Task.WaitAsync(cancellationToken);
        }
    }

    private static LogsViewModel.Source Source(string name, bool service) => service
        ? new LogsViewModel.ServiceSource(name)
        : new LogsViewModel.SiteSource(name);

    private string Text(bool service) => service
        ? Assert.IsType<LogsViewModel.ServiceContent>(viewModel.Current).Text
        : Assert.Single(Assert.IsType<LogsViewModel.SiteContent>(viewModel.Current).Entries).Message;

    private Task LoadAsync(LogsViewModel.Source source)
    {
        return viewModel.LoadSelectedAsync(source);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task OlderSourceCannotReplaceNewerSource(bool service)
    {
        var a = Source("a", service);
        var b = Source("b", service);
        var older = LoadAsync(a);
        var oldResponse = await handler.NextAsync();
        var newer = LoadAsync(b);
        var newResponse = await handler.NextAsync();
        newResponse.Log(b, "B contents");
        await newer;
        Assert.Equal("B contents", Text(service));

        var notifications = 0;
        viewModel.PropertyChanged += (_, _) => notifications++;
        oldResponse.Log(a, "A contents");
        await older;
        Assert.Equal("B contents", Text(service));
        Assert.Equal(0, notifications);
    }

    [Fact]
    public async Task OlderRefreshOfTheSameSourceCannotReplaceNewerData()
    {
        var source = new LogsViewModel.SiteSource("a");
        var older = LoadAsync(source);
        var oldResponse = await handler.NextAsync();
        var newer = LoadAsync(source);
        var newResponse = await handler.NextAsync();
        newResponse.Log(source, "new contents");
        await newer;
        oldResponse.Log(source, "old contents");
        await older;
        Assert.Equal("new contents", Text(service: false));
    }

    [Fact]
    public async Task StaleFailureCannotReplaceTheCurrentSourcesSuccess()
    {
        var older = LoadAsync(new LogsViewModel.SiteSource("a"));
        var oldResponse = await handler.NextAsync();
        var b = new LogsViewModel.SiteSource("b");
        var newer = LoadAsync(b);
        var newResponse = await handler.NextAsync();
        newResponse.Log(b, "B contents");
        await newer;
        oldResponse.Fail("A failed");
        await older;
        Assert.Null(viewModel.Error);
        Assert.Equal("B contents", Text(service: false));
    }

    [Fact]
    public async Task StaleSuccessCannotClearTheCurrentSourcesFailure()
    {
        var a = new LogsViewModel.SiteSource("a");
        var older = LoadAsync(a);
        var oldResponse = await handler.NextAsync();
        var newer = LoadAsync(new LogsViewModel.SiteSource("b"));
        var newResponse = await handler.NextAsync();
        newResponse.Fail("B failed");
        await newer;
        oldResponse.Log(a, "A contents");
        await older;
        Assert.Equal("B failed", viewModel.Error);
    }

    [Fact]
    public async Task RequestDeadlineFiresOnlyWhenTheRequestClockTicks()
    {
        var load = LoadAsync(new LogsViewModel.SiteSource("a"));
        await handler.NextAsync();

        requestClock.Advance(TimeSpan.FromSeconds(4));
        Assert.False(load.IsCompleted);

        requestClock.Advance(TimeSpan.FromSeconds(1));
        await load.WaitAsync(TimeSpan.FromSeconds(60));

        Assert.Equal("Flock did not answer GET /v1/logs/sites/a?limit=200 in time", viewModel.LoadError);
        Assert.Null(viewModel.Current);
        Assert.False(viewModel.IsLoading);
    }

    [Fact]
    public async Task ClearingAnOldSiteDoesNotEraseTheCurrentSite()
    {
        var a = new LogsViewModel.SiteSource("a");
        var loadA = LoadAsync(a);
        (await handler.NextAsync()).Log(a, "A contents");
        await loadA;
        var clearA = viewModel.ClearAsync("a");
        var clearResponse = await handler.NextAsync();
        Assert.Equal(HttpMethod.Delete, clearResponse.Method);
        Assert.Equal("/v1/logs/sites/a", clearResponse.Path);

        var b = new LogsViewModel.SiteSource("b");
        var loadB = LoadAsync(b);
        (await handler.NextAsync()).Log(b, "B contents");
        await loadB;
        clearResponse.Succeed("");
        (await handler.NextAsync()).Log(a, "A contents");
        await clearA;
        Assert.Equal("B contents", Text(service: false));
    }

    [Fact]
    public async Task APreClearReadCannotRestoreDeletedEntries()
    {
        var source = new LogsViewModel.SiteSource("a");
        var load = LoadAsync(source);
        var loadResponse = await handler.NextAsync();
        var clear = viewModel.ClearAsync("a");
        (await handler.NextAsync()).Succeed("");
        (await handler.NextAsync()).Succeed("[]");
        await clear;
        loadResponse.Log(source, "deleted contents");
        await load;
        Assert.Empty(Assert.IsType<LogsViewModel.SiteContent>(viewModel.Current).Entries);
    }

    [Fact]
    public async Task SiteAndServiceSourcesWithTheSameNameKeepTheirTypedContent()
    {
        var site = new LogsViewModel.SiteSource("same");
        var siteLoad = LoadAsync(site);
        (await handler.NextAsync()).Log(site, "site contents");
        await siteLoad;

        var service = new LogsViewModel.ServiceSource("same");
        var serviceLoad = LoadAsync(service);
        (await handler.NextAsync()).Log(service, "service contents");
        await serviceLoad;

        Assert.IsType<LogsViewModel.ServiceContent>(viewModel.Current);
        Assert.Equal("service contents", Text(service: true));
    }

    [Fact]
    public async Task OlderShowSourceCannotReselectAfterAChangedIndexIntent()
    {
        var first = viewModel.ShowSourceAsync(new LogsViewModel.SiteSource("a"));
        var index = await handler.NextAsync();
        var second = viewModel.ShowSourceAsync(new LogsViewModel.ServiceSource("b"));

        index.Succeed("{\"sites\":[\"a\"],\"services\":[\"b\"]}");
        var log = await handler.NextAsync();
        log.Log(new LogsViewModel.ServiceSource("b"), "B contents");
        await Task.WhenAll(first, second);

        Assert.Equal(new LogsViewModel.ServiceSource("b"), viewModel.Selection);
        Assert.Equal("B contents", Text(service: true));
    }

    [Fact]
    public async Task AToBToAKeepsTheNewestARead()
    {
        var a = new LogsViewModel.SiteSource("a");
        var first = LoadAsync(a);
        var firstResponse = await handler.NextAsync();
        var b = new LogsViewModel.SiteSource("b");
        var middle = LoadAsync(b);
        var middleResponse = await handler.NextAsync();
        var last = LoadAsync(a);
        var lastResponse = await handler.NextAsync();

        lastResponse.Log(a, "new A contents");
        await last;
        firstResponse.Log(a, "old A contents");
        await Task.WhenAll(first, middle);

        Assert.Equal("new A contents", Text(service: false));
    }

    [Fact]
    public async Task UnchangedRefreshKeepsTheAcceptedContentReference()
    {
        var source = new LogsViewModel.ServiceSource("daemon");
        var first = LoadAsync(source);
        (await handler.NextAsync()).Log(source, "same contents");
        await first;
        var accepted = viewModel.Current;

        var second = LoadAsync(source);
        (await handler.NextAsync()).Log(source, "same contents");
        await second;

        Assert.Same(accepted, viewModel.Current);
    }

    [Fact]
    public async Task FilterSetWhileLoadingAppliesToTheAcceptedSiteResult()
    {
        var source = new LogsViewModel.SiteSource("a");
        var load = LoadAsync(source);
        viewModel.Query = "needle";
        var response = await handler.NextAsync();
        response.Succeed(JsonSerializer.Serialize(new[]
        {
            new LogEntry { Message = "needle" },
            new LogEntry { Message = "other" },
        }, FlockJson.Options));
        await load;

        Assert.Single(viewModel.FilteredEntries);
        Assert.Equal("needle", viewModel.FilteredEntries[0].Message);
    }

    [Fact]
    public async Task FailedClearKeepsItsActionErrorWhileReconciledReadSucceeds()
    {
        var source = new LogsViewModel.SiteSource("a");
        var load = LoadAsync(source);
        (await handler.NextAsync()).Log(source, "before clear");
        await load;

        var clear = viewModel.ClearAsync(source);
        var clearResponse = await handler.NextAsync();
        clearResponse.Fail("clear failed");
        (await handler.NextAsync()).Log(source, "remote contents");
        var outcome = await clear;

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal("clear failed", viewModel.ActionError);
        Assert.Equal("remote contents", Text(service: false));
    }

    [Fact]
    public async Task DisposedWindowCannotPublishItsRetiredRead()
    {
        var source = new LogsViewModel.SiteSource("a");
        var load = LoadAsync(source);
        var response = await handler.NextAsync();

        await viewModel.DisposeAsync();
        response.Log(source, "late contents");
        await load;

        Assert.Null(viewModel.Current);
    }

    [Fact]
    public async Task AutoRefreshUsesTheOwnedTimeProviderTimer()
    {
        var clock = new ManualTimeProvider();
        var polled = new LogsViewModel(client, new OperationRunner(), timeProvider: clock);
        var source = new LogsViewModel.ServiceSource("daemon");
        var load = polled.LoadSelectedAsync(source);
        (await handler.NextAsync()).Log(source, "before refresh");
        await load;

        var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        polled.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName == nameof(LogsViewModel.Current) && polled.Current is LogsViewModel.ServiceContent { Text: "after refresh" })
            {
                changed.TrySetResult();
            }
        };
        polled.AutoRefreshEnabled = true;
        clock.Tick();
        (await handler.NextAsync()).Log(source, "after refresh");
        await changed.Task.WaitAsync(TimeSpan.FromSeconds(60));
        await polled.DisposeAsync();

        Assert.Equal("after refresh", Assert.IsType<LogsViewModel.ServiceContent>(polled.Current).Text);
    }

    private sealed class ManualTimeProvider : TimeProvider
    {
        private ManualTimer? timer;

        public void Tick() => timer?.Fire();

        public override ITimer CreateTimer(TimerCallback callback, object? state, TimeSpan dueTime, TimeSpan period)
        {
            timer = new ManualTimer(callback, state);
            return timer;
        }

        private sealed class ManualTimer(TimerCallback callback, object? state) : ITimer
        {
            private bool disposed;

            public bool Change(TimeSpan dueTime, TimeSpan period) => !disposed;

            public void Fire()
            {
                if (!disposed)
                {
                    callback(state);
                }
            }

            public void Dispose() => disposed = true;

            public ValueTask DisposeAsync()
            {
                Dispose();
                return ValueTask.CompletedTask;
            }
        }
    }
}
