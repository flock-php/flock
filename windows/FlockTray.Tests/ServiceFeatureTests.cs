using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.Extensions.Time.Testing;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.Tests;

public sealed class ServiceFeatureTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-services-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;

    public ServiceFeatureTests()
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
    public async Task PortFailureDoesNotRunAnotherMutationAndRefreshesTheService()
    {
        await using var service = new ServicesService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.FailPatch = true;

        var outcome = await service.SetPortAsync("postgres", 5433);

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal(1, handler.Requests.Count(request => request == "PATCH /v1/services/postgres"));
        Assert.Equal(2, handler.Requests.Count(request => request == "GET /v1/services"));
        Assert.Contains("port unavailable", service.Error);
    }

    [Fact]
    public async Task AReadStartedBeforeAServiceWriteCannotPublishAfterTheWrite()
    {
        await using var service = new ServicesService(client, new OperationRunner());
        handler.BlockFirstServiceRead = true;
        var stale = service.RefreshAsync();
        await handler.FirstServiceReadStarted.Task;

        var write = service.SetPortAsync("postgres", 5433);
        await write;
        handler.ReleaseFirstServiceRead.TrySetResult();
        await stale;

        Assert.Equal(5433, service.Current.Single().Port);
    }

    [Fact]
    public async Task FirstPostgresInstallStopsAfterARejectedPortPrerequisite()
    {
        await using var service = new PostgresService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.FailPatch = true;

        var outcome = await service.InstallFirstAsync("18", 5433, 5432);

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Contains("port unavailable", service.Error);
        Assert.DoesNotContain("POST /v1/postgres/versions/18/install", handler.Requests);
        Assert.Equal(2, handler.Requests.Count(request => request == "GET /v1/postgres/versions"));
    }

    [Fact]
    public async Task PostgresMutationLaneRejectsASecondWriteBeforeItsDelegateRuns()
    {
        await using var service = new PostgresService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockPostgres = true;

        var first = service.InstallAsync("18");
        await handler.PostgresWriteStarted.Task;
        var second = await service.UpdateAsync("18");

        Assert.Equal(OperationOutcome.Rejected, second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/postgres/versions/18/install"));
        handler.ReleasePostgresWrite.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
    }

    [Fact]
    public async Task CatalogLanesAllowDifferentServicesButConflictWithinOneService()
    {
        await using var service = new CatalogFeatureService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockCatalog = true;

        var mysql = service.InstallAsync("mysql", "8.4");
        await handler.MySqlWriteStarted.Task;
        var redis = service.InstallAsync("redis", "7");
        await handler.RedisWriteStarted.Task;
        var duplicate = await service.ActivateAsync("mysql", "9");

        Assert.Equal(OperationOutcome.Rejected, duplicate);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/redis/install"));
        handler.ReleaseMySqlWrite.TrySetResult();
        handler.ReleaseRedisWrite.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await mysql);
        Assert.Equal(OperationOutcome.Succeeded, await redis);
    }

    [Fact]
    public async Task CatalogBatchRejectsAnInstallThatAlreadyReservedTheService()
    {
        handler.CatalogBody = CatalogWithUpdates();
        await using var service = new CatalogFeatureService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockCatalog = true;

        var install = service.InstallAsync("mysql", "8.4");
        await handler.MySqlWriteStarted.Task;

        Assert.Equal(OperationOutcome.Rejected, await service.InstallUpdatesAsync("mysql", ["8.4", "9"]));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));

        handler.ReleaseMySqlWrite.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await install);
    }

    [Fact]
    public async Task CatalogDraftSurvivesAJobSnapshotUpdate()
    {
        await using var service = new CatalogFeatureService(client, new OperationRunner());
        await service.RefreshAsync();
        var row = new CatalogServiceViewModel(service.Current!["mysql"]!);
        row.DraftLine = "9";
        row.DraftPort = 3307;

        row.Update(new Flock.Kit.CatalogService
        {
            Id = "mysql",
            Name = "MySQL",
            Port = 3306,
            Lines = [new CatalogLine { Id = "8.4", Label = "8.4", Installed = false }],
            Job = new CatalogJob { State = "running", Action = "install" },
        });

        Assert.Equal("9", row.DraftLine);
        Assert.Equal(3307, row.DraftPort);
    }

    [Fact]
    public async Task RefreshNormalizesDisappearingInstallDraftsBeforeDispatchingFallbackTargets()
    {
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        var actions = new ServiceReleaseActions(postgres, catalog);
        var rows = new ServicesPaneViewModel(services, postgres, catalog, actions);

        handler.PostgresVersionsBody = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.2\"},{\"major\":\"17\",\"latest\":\"17.6\"}]}";
        await postgres.RefreshAsync();
        rows.PostgresDraftMajor = "17";
        handler.PostgresVersionsBody = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.2\"}]}";
        await postgres.RefreshAsync();

        Assert.Equal("18", rows.PostgresDraftMajor);
        Assert.Equal(OperationOutcome.Succeeded, await actions.InstallFirstAsync("postgres", rows.PostgresDraftMajor!, rows.PostgresDraftPort, 5432));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/postgres/versions/18/install"));

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"port\":3306,\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"},{\"id\":\"9\",\"label\":\"9\"}]}]}";
        await catalog.RefreshAsync();
        var mysql = Assert.Single(rows.CatalogServices);
        mysql.DraftLine = "9";
        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"port\":3306,\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"}]}]}";
        await catalog.RefreshAsync();

        Assert.Equal("8.4", mysql.DraftLine);
        Assert.Equal(OperationOutcome.Succeeded, await actions.InstallFirstAsync("mysql", mysql.DraftLine!, mysql.DraftPort, 3306));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        rows.Dispose();
    }

    [Theory]
    [InlineData("postgres", true, true, false, true)]
    [InlineData("postgres", true, false, true, false)]
    [InlineData("mysql", true, false, true, true)]
    [InlineData("mysql", true, true, false, false)]
    [InlineData("mysql", false, true, true, false)]
    public void InstallDeepLinksWaitOnlyForTheirRequiredSnapshots(string service, bool servicesReady, bool postgresReady, bool catalogReady, bool expected)
    {
        Assert.Equal(expected, DeepLinkCoordinator.HasRequiredSnapshots(new InstallLink(service), servicesReady, postgresReady, catalogReady));
    }

    [Fact]
    public async Task ReleaseActionsResolveCurrentAndOneUpdateForPostgresAndCatalog()
    {
        handler.PostgresVersionsBody = "{\"checked_at\":\"2026-10-25T12:00:00+00:00\",\"check_error\":\"\",\"port\":5432,\"active\":\"18\",\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"active\":true}]}";
        handler.CatalogBody = "{\"checked_at\":\"2026-10-25T12:00:00+00:00\",\"check_error\":\"\",\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":\"8.4.1\",\"line\":\"8.4\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\",\"update\":true,\"active\":true}]}]}";
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await postgres.RefreshAsync();
        await catalog.RefreshAsync();
        var actions = new ServiceReleaseActions(postgres, catalog);

        handler.PostgresVersionsBody = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.2\"}]}";
        await postgres.RefreshAsync();
        var postgresInstall = actions.Resolve("postgres");
        Assert.Equal(ServiceReleaseActionKind.Install, postgresInstall.Kind);
        Assert.Equal("18", Assert.Single(postgresInstall.Targets).Line);

        handler.PostgresVersionsBody = "{\"checked_at\":\"2026-10-25T12:00:00+00:00\",\"check_error\":\"\",\"port\":5432,\"active\":\"18\",\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"active\":true}]}";
        await postgres.RefreshAsync();
        var postgresAction = actions.Resolve("postgres");
        var catalogAction = actions.Resolve("mysql");

        Assert.Equal(ServiceReleaseActionKind.Update, postgresAction.Kind);
        Assert.Equal("18", Assert.Single(postgresAction.Targets).Line);
        Assert.Equal(ServiceReleaseActionKind.Update, catalogAction.Kind);
        Assert.Equal("8.4", Assert.Single(catalogAction.Targets).Line);
        Assert.Equal(DateTimeOffset.Parse("2026-10-25T12:00:00+00:00"), postgresAction.CheckedAt);
        Assert.Empty(postgresAction.CheckError);

        Assert.Equal(OperationOutcome.Succeeded, await actions.RunTargetAsync(postgresAction.Targets[0]));
        Assert.Contains("POST /v1/postgres/versions/18/update", handler.Requests);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunTargetAsync(catalogAction.Targets[0]));
        Assert.Contains("POST /v1/catalog/mysql/install", handler.Requests);
    }

    [Fact]
    public async Task PostgresRowsExposeOnlyCurrentLifecycleCommandsAndRejectStaleCommands()
    {
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        var actions = new ServiceReleaseActions(postgres, catalog);

        handler.PostgresVersionsBody = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.2\"}]}";
        await postgres.RefreshAsync();
        var uninstalled = Assert.Single(actions.ResolveRow("postgres").Lines);
        Assert.Equal([ServiceRowCommandKind.Install], uninstalled.PrimaryCommands);
        Assert.Empty(uninstalled.MoreCommands);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(uninstalled.Target, ServiceRowCommandKind.Install));
        Assert.Contains("POST /v1/postgres/versions/18/install", handler.Requests);

        handler.Requests.Clear();
        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.1\",\"active\":true}]}";
        await postgres.RefreshAsync();
        var current = Assert.Single(actions.ResolveRow("postgres").Lines);
        Assert.Equal([ServiceRowCommandKind.Uninstall], current.PrimaryCommands);
        Assert.Empty(current.MoreCommands);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(current.Target, ServiceRowCommandKind.Uninstall));
        Assert.Contains("DELETE /v1/postgres/versions/18", handler.Requests);

        handler.Requests.Clear();
        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"active\":true}]}";
        await postgres.RefreshAsync();
        var update = Assert.Single(actions.ResolveRow("postgres").Lines);
        Assert.Equal([ServiceRowCommandKind.Update], update.PrimaryCommands);
        Assert.Equal([ServiceRowCommandKind.Uninstall], update.MoreCommands);
        Assert.Equal(update.Target, Assert.Single(actions.Resolve("postgres").Targets));
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(update.Target, ServiceRowCommandKind.Update));
        Assert.Contains("POST /v1/postgres/versions/18/update", handler.Requests);

        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.1\",\"active\":true}]}";
        await postgres.RefreshAsync();
        handler.Requests.Clear();
        Assert.Equal(OperationOutcome.Rejected, await actions.RunAsync(update.Target, ServiceRowCommandKind.Update));
        Assert.DoesNotContain("POST /v1/postgres/versions/18/update", handler.Requests);

        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.1\",\"active\":true},{\"major\":\"17\",\"installed\":\"17.1\",\"latest\":\"17.1\"}]}";
        await postgres.RefreshAsync();
        var inactive = actions.ResolveRow("postgres").Lines.Single(line => line.Target.Line == "17");
        Assert.Equal([ServiceRowCommandKind.Uninstall], inactive.PrimaryCommands);
        Assert.Equal([ServiceRowCommandKind.Activate], inactive.MoreCommands);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(inactive.Target, ServiceRowCommandKind.Activate));
        Assert.Contains("POST /v1/postgres/versions/17/activate", handler.Requests);

        handler.PostgresVersionsBody = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"busy\":\"updating\"}]}";
        await postgres.RefreshAsync();
        var busy = Assert.Single(actions.ResolveRow("postgres").Lines);
        Assert.Empty(busy.PrimaryCommands);
        Assert.Empty(busy.MoreCommands);
        Assert.Equal("Updating…", busy.BusyText);
    }

    [Fact]
    public async Task CatalogRowsExposeCurrentCommandsAndDispatchJobRecovery()
    {
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        var actions = new ServiceReleaseActions(postgres, catalog);

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"latest\":\"8.4.7\"}]}]}";
        await catalog.RefreshAsync();
        var uninstalled = Assert.Single(actions.ResolveRow("mysql").Lines);
        Assert.Equal([ServiceRowCommandKind.Install], uninstalled.PrimaryCommands);
        Assert.Empty(uninstalled.MoreCommands);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(uninstalled.Target, ServiceRowCommandKind.Install));
        Assert.Contains("POST /v1/catalog/mysql/install", handler.Requests);

        handler.Requests.Clear();
        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":\"8.4.7\",\"line\":\"8.4\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.7\",\"active\":true}]}]}";
        await catalog.RefreshAsync();
        var current = Assert.Single(actions.ResolveRow("mysql").Lines);
        Assert.Equal([ServiceRowCommandKind.Uninstall], current.PrimaryCommands);
        Assert.Equal([ServiceRowCommandKind.Reinstall, ServiceRowCommandKind.ShowLog], current.MoreCommands);

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":\"8.4.7\",\"line\":\"8.4\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.7\",\"latest\":\"8.4.8\",\"update\":true}]}]}";
        await catalog.RefreshAsync();
        var update = Assert.Single(actions.ResolveRow("mysql").Lines);
        Assert.Equal([ServiceRowCommandKind.Update], update.PrimaryCommands);
        Assert.Equal([ServiceRowCommandKind.Uninstall, ServiceRowCommandKind.Activate, ServiceRowCommandKind.ShowLog], update.MoreCommands);

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"}],\"job\":{\"action\":\"install\",\"state\":\"running\",\"line\":\"8.4\"}}]}";
        await catalog.RefreshAsync();
        var running = actions.ResolveRow("mysql");
        Assert.Empty(Assert.Single(running.Lines).PrimaryCommands);
        Assert.Empty(Assert.Single(running.Lines).MoreCommands);
        Assert.Equal([ServiceRowCommandKind.ShowLog, ServiceRowCommandKind.Cancel], running.JobCommands);
        var runningTarget = new ServiceReleaseTarget("mysql", ServiceReleaseSource.Catalog, "8.4", "8.4", "");
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(runningTarget, ServiceRowCommandKind.Cancel));
        Assert.Contains("POST /v1/catalog/mysql/cancel", handler.Requests);

        handler.Requests.Clear();
        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"},{\"id\":\"9\",\"label\":\"9\"}],\"job\":{\"action\":\"install\",\"state\":\"running\",\"line\":\"9\"}}]}";
        await catalog.RefreshAsync();
        Assert.Equal(OperationOutcome.Rejected, await actions.RunAsync(runningTarget, ServiceRowCommandKind.Cancel));
        Assert.DoesNotContain("POST /v1/catalog/mysql/cancel", handler.Requests);

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"}],\"job\":{\"action\":\"install\",\"state\":\"failed\",\"line\":\"8.4\",\"error\":\"download failed\"}}]}";
        await catalog.RefreshAsync();
        var failed = actions.ResolveRow("mysql");
        Assert.Empty(Assert.Single(failed.Lines).PrimaryCommands);
        Assert.Empty(Assert.Single(failed.Lines).MoreCommands);
        Assert.Equal([ServiceRowCommandKind.Retry, ServiceRowCommandKind.Dismiss, ServiceRowCommandKind.ShowLog], failed.JobCommands);
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(runningTarget, ServiceRowCommandKind.Retry));
        Assert.Contains("POST /v1/catalog/mysql/install", handler.Requests);

        await catalog.RefreshAsync();
        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAsync(runningTarget, ServiceRowCommandKind.Dismiss));
        Assert.Contains("DELETE /v1/catalog/mysql/job", handler.Requests);
    }

    [Fact]
    public async Task ReleaseActionsShowInstallForAnUninstalledCatalogAndCheckErrorsWhenCurrent()
    {
        handler.CatalogBody = "{\"checked_at\":\"2026-10-25T12:00:00+00:00\",\"check_error\":\"manifest unavailable\",\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"latest\":\"8.4.7\"}]}]}";
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await catalog.RefreshAsync();
        await using var postgres = new PostgresService(client, new OperationRunner());
        var actions = new ServiceReleaseActions(postgres, catalog);

        var install = actions.Resolve("mysql");

        Assert.Equal(ServiceReleaseActionKind.Install, install.Kind);
        Assert.True(install.IsEnabled);
        Assert.Equal("8.4", Assert.Single(install.Targets).Line);

        handler.CatalogBody = "{\"checked_at\":\"2026-10-25T12:00:00+00:00\",\"check_error\":\"manifest unavailable\",\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":\"8.4.1\",\"line\":\"8.4\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\",\"active\":true}]}]}";
        await catalog.RefreshAsync();
        var check = actions.Resolve("mysql");

        Assert.Equal(ServiceReleaseActionKind.Check, check.Kind);
        Assert.True(check.IsEnabled);
        Assert.Equal("manifest unavailable", check.CheckError);
        Assert.NotNull(check.CheckedAt);
    }

    [Fact]
    public async Task ReleaseActionsResolveAllUpdatesWithOrderedTargets()
    {
        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"active\":true},{\"major\":\"17\",\"installed\":\"17.1\",\"latest\":\"17.2\",\"update\":true,\"active\":false}]}";
        handler.CatalogBody = CatalogWithUpdates();
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await postgres.RefreshAsync();
        await catalog.RefreshAsync();
        var actions = new ServiceReleaseActions(postgres, catalog);

        var postgresAction = actions.Resolve("postgres");
        var catalogAction = actions.Resolve("mysql");

        Assert.Equal(ServiceReleaseActionKind.UpdateAll, postgresAction.Kind);
        Assert.Equal("Install all updates (2)", postgresAction.AccessibleLabel);
        Assert.Equal(["18", "17"], postgresAction.Targets.Select(target => target.Line));
        Assert.Equal(ServiceReleaseActionKind.UpdateAll, catalogAction.Kind);
        Assert.Equal("Install all updates (2)", catalogAction.AccessibleLabel);
        Assert.Equal(["8.4", "9"], catalogAction.Targets.Select(target => target.Line));
    }

    [Fact]
    public async Task ReleaseActionsDisableWhilePostgresMutationOrCatalogJobRuns()
    {
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await postgres.RefreshAsync();
        await catalog.RefreshAsync();
        var actions = new ServiceReleaseActions(postgres, catalog);

        handler.BlockPostgres = true;
        var postgresMutation = actions.RunTargetAsync(new ServiceReleaseTarget("postgres", ServiceReleaseSource.Postgres, "18", "18", "18.1"));
        await handler.PostgresWriteStarted.Task;
        var postgresBusy = actions.Resolve("postgres");
        Assert.Equal(ServiceReleaseActionKind.Busy, postgresBusy.Kind);
        Assert.False(postgresBusy.IsEnabled);
        handler.ReleasePostgresWrite.TrySetResult();
        await postgresMutation;

        handler.CatalogBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"}],\"job\":{\"action\":\"install\",\"state\":\"running\",\"phase\":\"downloading\"}}]}";
        await catalog.RefreshAsync();
        var catalogBusy = actions.Resolve("mysql");
        Assert.Equal(ServiceReleaseActionKind.Busy, catalogBusy.Kind);
        Assert.False(catalogBusy.IsEnabled);
    }

    [Fact]
    public async Task CatalogInstallResponsePublishesItsRunningJobBeforeMutationEnds()
    {
        handler.CatalogInstallBody = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"}],\"job\":{\"action\":\"install\",\"state\":\"running\",\"phase\":\"downloading\"}}]}";
        handler.CatalogBody = handler.CatalogInstallBody;
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await catalog.RefreshAsync();
        await using var postgres = new PostgresService(client, new OperationRunner());
        var actions = new ServiceReleaseActions(postgres, catalog);

        Assert.Equal(OperationOutcome.Succeeded, await actions.InstallDeepLinkAsync("mysql", "8.4"));

        var resolved = actions.Resolve("mysql");
        Assert.Equal(ServiceReleaseActionKind.Busy, resolved.Kind);
        Assert.False(resolved.IsEnabled);
    }

    [Fact]
    public async Task ReleaseRefreshSkipsServicesReadWhileIdleRefreshKeepsIt()
    {
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        var rows = new ServicesPaneViewModel(services, postgres, catalog, new ServiceReleaseActions(postgres, catalog));
        handler.Requests.Clear();

        await rows.RefreshReleaseStateAsync();
        Assert.DoesNotContain("GET /v1/services", handler.Requests);
        Assert.Contains("GET /v1/postgres/versions", handler.Requests);
        Assert.Contains("GET /v1/catalog", handler.Requests);

        handler.Requests.Clear();
        await rows.RefreshAsync();
        Assert.Contains("GET /v1/services", handler.Requests);
        rows.Dispose();
    }

    [Fact]
    public async Task ServiceSelectionStaysInsideTheVisibleSnapshot()
    {
        handler.ServicesBody = "[{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"installed\":true},{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":false},{\"id\":\"redis\",\"name\":\"Redis\",\"installed\":true}]";
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        var rows = new ServicesPaneViewModel(services, postgres, catalog, new ServiceReleaseActions(postgres, catalog));
        await services.RefreshAsync();

        rows.SelectedId = "mysql";
        rows.ShowAll = false;

        Assert.Equal("postgres", rows.SelectedId);
        Assert.Equal("postgres", rows.SelectedService?.Id);
        Assert.DoesNotContain(rows.VisibleServices, service => service.Id == "mysql");

        handler.ServicesBody = "[{\"id\":\"redis\",\"name\":\"Redis\",\"installed\":true}]";
        await services.RefreshAsync();

        Assert.Equal("redis", rows.SelectedId);
        Assert.Equal("redis", rows.SelectedService?.Id);
        rows.Dispose();
    }

    [Fact]
    public async Task VisibleServicesKeepTheirCategorySections()
    {
        handler.ServicesBody = "[{\"id\":\"web\",\"name\":\"Web server\",\"category\":\"Core\",\"installed\":true},{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"category\":\"Database\",\"installed\":true},{\"id\":\"mysql\",\"name\":\"MySQL\",\"category\":\"Database\",\"installed\":true}]";
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await services.RefreshAsync();
        var rows = new ServicesPaneViewModel(services, postgres, catalog, new ServiceReleaseActions(postgres, catalog));

        var groups = rows.VisibleServiceGroups.ToList();

        Assert.Equal(["Core", "Database"], groups.Select(group => group.Name));
        Assert.Equal(["postgres", "mysql"], groups[1].Services.Select(service => service.Id));
        rows.Dispose();
    }

    [Fact]
    public void ServiceListDetailOmitsEmptyValuesAndShowsPortAndStoppedState()
    {
        Assert.Equal("", new ServiceViewModel(new Service { Id = "hosts", Running = true }).ListDetail);
        Assert.Equal("Port 53 · Stopped", new ServiceViewModel(new Service { Id = "dns", Port = 53 }).ListDetail);
        Assert.Equal("8.4.13 · Port 3306", new ServiceViewModel(new Service { Id = "mysql", Version = "8.4.13", Port = 3306, Running = true }).ListDetail);
    }

    [Fact]
    public async Task RefreshingTheSelectedServicePublishesItsUpdatedDetail()
    {
        handler.ServicesBody = "[{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"running\":false}]";
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await services.RefreshAsync();
        var rows = new ServicesPaneViewModel(services, postgres, catalog, new ServiceReleaseActions(postgres, catalog));
        var notifications = 0;
        rows.SelectedServiceChanged += _ => notifications++;

        handler.ServicesBody = "[{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"running\":true}]";
        await services.RefreshAsync();

        Assert.Equal(1, notifications);
        Assert.True(rows.SelectedService is { Running: true });
        rows.Dispose();
    }

    [Fact]
    public async Task RefreshingTheSelectedCatalogPublishesItsUpdatedDetail()
    {
        handler.ServicesBody = "[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":true,\"on_request\":true}]";
        await using var services = new ServicesService(client, new OperationRunner());
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await services.RefreshAsync();
        await catalog.RefreshAsync();
        var rows = new ServicesPaneViewModel(services, postgres, catalog, new ServiceReleaseActions(postgres, catalog)) { SelectedId = "mysql" };
        var notifications = 0;
        rows.SelectedServiceChanged += _ => notifications++;

        handler.CatalogBody = "{\"checked_at\":\"2026-10-26T12:00:00+00:00\",\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"installed\":\"8.4.1\",\"line\":\"8.4\",\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\"}]}]}";
        await catalog.RefreshAsync();

        Assert.Equal(1, notifications);
        rows.Dispose();
    }

    [Fact]
    public async Task CatalogBatchWaitsForEachJobAndStartsTargetsInOrder()
    {
        var clock = new FakeTimeProvider();
        handler.CatalogBody = CatalogWithUpdates();
        await using var catalog = new CatalogFeatureService(client, new OperationRunner(), timeProvider: clock);
        await catalog.RefreshAsync();
        handler.Requests.Clear();
        handler.CatalogInstallBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogCompleted("9"));
        handler.CatalogBodies.Enqueue(CatalogCompleted("9"));
        handler.CatalogBodies.Enqueue(CatalogCompleted("9"));
        handler.CatalogInstallBodies.Enqueue(CatalogRunning("9"));
        handler.CatalogBodies.Enqueue(CatalogRunning("9"));
        handler.CatalogBodies.Enqueue(CatalogCompleted());

        var batch = catalog.InstallUpdatesAsync("mysql", ["8.4", "9"]);
        await WaitForRequestAsync(handler, "POST /v1/catalog/mysql/install");
        await WaitForCatalogReadsAsync(handler, 2);

        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        Assert.True(catalog.IsRunning("mysql"));

        clock.Advance(TimeSpan.FromSeconds(2));
        await WaitForCatalogReadsAsync(handler, 3);
        await WaitForRequestCountAsync(handler, "POST /v1/catalog/mysql/install", 2);
        await WaitForCatalogReadsAsync(handler, 5);
        clock.Advance(TimeSpan.FromSeconds(2));
        await WaitForCatalogReadsAsync(handler, 6);

        Assert.Equal(OperationOutcome.Succeeded, await batch);
        Assert.Equal(2, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        Assert.Equal(2, handler.CatalogInstallRequests.Count);
        Assert.Contains("\"line\":\"8.4\"", handler.CatalogInstallRequests[0]);
        Assert.Contains("\"line\":\"9\"", handler.CatalogInstallRequests[1]);
        Assert.False(catalog.IsRunning("mysql"));
    }

    [Fact]
    public async Task CatalogBatchStopsOnFailedJobAndRetainsRecoveryState()
    {
        var clock = new FakeTimeProvider();
        handler.CatalogBody = CatalogWithUpdates();
        await using var catalog = new CatalogFeatureService(client, new OperationRunner(), timeProvider: clock);
        await catalog.RefreshAsync();
        handler.CatalogInstallBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogFailed("8.4"));

        var batch = catalog.InstallUpdatesAsync("mysql", ["8.4", "9"]);
        await WaitForCatalogReadsAsync(handler, 2);
        clock.Advance(TimeSpan.FromSeconds(2));
        await WaitForCatalogReadsAsync(handler, 3);

        Assert.Equal(OperationOutcome.Succeeded, await batch);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        Assert.True(catalog.Current?["mysql"]?.Job?.IsFailed);
        Assert.Null(catalog.ActionError);
    }

    [Fact]
    public async Task CatalogBatchStopsWithAnErrorWhenACompletedJobLeavesTheUpdate()
    {
        var clock = new FakeTimeProvider();
        handler.CatalogBody = CatalogWithUpdates();
        await using var catalog = new CatalogFeatureService(client, new OperationRunner(), timeProvider: clock);
        await catalog.RefreshAsync();
        handler.CatalogInstallBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogWithUpdates());

        var batch = catalog.InstallUpdatesAsync("mysql", ["8.4", "9"]);
        await WaitForCatalogReadsAsync(handler, 2);
        clock.Advance(TimeSpan.FromSeconds(2));
        await WaitForCatalogReadsAsync(handler, 3);

        Assert.Equal(OperationOutcome.Failed, await batch);
        Assert.Contains("did not finish updating", catalog.ActionError);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
    }

    [Fact]
    public async Task CatalogBatchStopsWithAnErrorWhenARequestedTargetIsMissing()
    {
        var clock = new FakeTimeProvider();
        handler.CatalogBody = CatalogWithUpdates().Replace("\"id\":\"9\",", "\"id\":\"other\",");
        await using var catalog = new CatalogFeatureService(client, new OperationRunner(), timeProvider: clock);
        await catalog.RefreshAsync();

        var outcome = await catalog.InstallUpdatesAsync("mysql", ["9"]);

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Contains("is no longer installed", catalog.ActionError);
        Assert.DoesNotContain("POST /v1/catalog/mysql/install", handler.Requests);
    }

    [Fact]
    public async Task CatalogBatchCancellationStopsQuietlyAndLeavesCancelAvailableDuringPolling()
    {
        var clock = new FakeTimeProvider();
        handler.CatalogBody = CatalogWithUpdates();
        await using var catalog = new CatalogFeatureService(client, new OperationRunner(), timeProvider: clock);
        await catalog.RefreshAsync();
        handler.CatalogInstallBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogBodies.Enqueue(CatalogRunning("8.4"));
        handler.CatalogCancelBody = CatalogCompleted("8.4", "9");
        handler.CatalogBodies.Enqueue(CatalogCompleted("8.4", "9"));

        var batch = catalog.InstallUpdatesAsync("mysql", ["8.4", "9"]);
        await WaitForRequestAsync(handler, "POST /v1/catalog/mysql/install");
        await WaitForCatalogReadsAsync(handler, 2);
        Assert.True(catalog.IsRunning("mysql"));
        Assert.True(catalog.CanCancelJob("mysql"));

        Assert.Equal(OperationOutcome.Succeeded, await catalog.CancelAsync("mysql"));
        clock.Advance(TimeSpan.FromSeconds(2));
        Assert.Equal(OperationOutcome.Succeeded, await batch);
        Assert.Null(catalog.ActionError);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
    }

    [Fact]
    public async Task ReleaseActionsRunPostgresUpdatesInOrderAndStopOnFirstFailure()
    {
        handler.PostgresVersionsBody = "{\"active\":\"18\",\"port\":5432,\"majors\":[{\"major\":\"18\",\"installed\":\"18.1\",\"latest\":\"18.2\",\"update\":true,\"active\":true},{\"major\":\"17\",\"installed\":\"17.1\",\"latest\":\"17.2\",\"update\":true,\"active\":false}]}";
        await using var postgres = new PostgresService(client, new OperationRunner());
        await using var catalog = new CatalogFeatureService(client, new OperationRunner());
        await postgres.RefreshAsync();
        var actions = new ServiceReleaseActions(postgres, catalog);

        Assert.Equal(OperationOutcome.Succeeded, await actions.RunAllUpdatesAsync("postgres"));
        var updates = handler.Requests.Where(request => request.Contains("/update", StringComparison.Ordinal)).ToList();
        Assert.Equal(["POST /v1/postgres/versions/18/update", "POST /v1/postgres/versions/17/update"], updates);

        handler.Requests.Clear();
        handler.FailPostgresUpdateMajor = "18";
        Assert.Equal(OperationOutcome.Failed, await actions.RunAllUpdatesAsync("postgres"));
        Assert.Equal(["POST /v1/postgres/versions/18/update"], handler.Requests.Where(request => request.Contains("/update", StringComparison.Ordinal)).ToList());
        Assert.DoesNotContain("POST /v1/postgres/versions/17/update", handler.Requests);
    }

    private static async Task WaitForRequestAsync(Handler handler, string request)
    {
        for (var attempt = 0; attempt < 100 && !handler.Requests.Contains(request); attempt++)
        {
            await Task.Yield();
        }
        Assert.Contains(request, handler.Requests);
    }

    private static Task WaitForRequestCountAsync(Handler handler, string request, int count) =>
        WaitUntilAsync(() => handler.Requests.Count(item => item == request) >= count);

    private static Task WaitForCatalogReadsAsync(Handler handler, int count) =>
        WaitUntilAsync(() => handler.Requests.Count(item => item == "GET /v1/catalog") >= count);

    private static async Task WaitUntilAsync(Func<bool> condition)
    {
        for (var attempt = 0; attempt < 1000 && !condition(); attempt++)
        {
            await Task.Yield();
        }
        Assert.True(condition());
    }

    private static string CatalogWithUpdates() => "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"available\":true,\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\",\"latest\":\"8.4.2\",\"update\":true},{\"id\":\"9\",\"label\":\"9\",\"installed\":true,\"version\":\"9.0.1\",\"latest\":\"9.0.2\",\"update\":true}]}]}";

    private static string CatalogRunning(string line) => $"{{\"services\":[{{\"id\":\"mysql\",\"name\":\"MySQL\",\"available\":true,\"lines\":[{{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\",\"latest\":\"8.4.2\",\"update\":true}},{{\"id\":\"9\",\"label\":\"9\",\"installed\":true,\"version\":\"9.0.1\",\"latest\":\"9.0.2\",\"update\":true}}],\"job\":{{\"action\":\"update\",\"state\":\"running\",\"line\":\"{line}\"}}}}]}}";

    private static string CatalogCompleted(params string[] stillUpdating) => $"{{\"services\":[{{\"id\":\"mysql\",\"name\":\"MySQL\",\"available\":true,\"lines\":[{{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.2\",\"latest\":\"8.4.2\",\"update\":{stillUpdating.Contains("8.4").ToString().ToLowerInvariant()}}},{{\"id\":\"9\",\"label\":\"9\",\"installed\":true,\"version\":\"9.0.2\",\"latest\":\"9.0.2\",\"update\":{stillUpdating.Contains("9").ToString().ToLowerInvariant()}}}]}}]}}";

    private static string CatalogFailed(string line) => $"{{\"services\":[{{\"id\":\"mysql\",\"name\":\"MySQL\",\"available\":true,\"lines\":[{{\"id\":\"8.4\",\"label\":\"8.4\",\"installed\":true,\"version\":\"8.4.1\",\"latest\":\"8.4.2\",\"update\":true}},{{\"id\":\"9\",\"label\":\"9\",\"installed\":true,\"version\":\"9.0.1\",\"latest\":\"9.0.2\",\"update\":true}}],\"job\":{{\"action\":\"update\",\"state\":\"failed\",\"line\":\"{line}\",\"error\":\"download failed\"}}}}]}}";

    private sealed class Handler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];
        public bool FailPatch { get; set; }
        public bool BlockFirstServiceRead { get; set; }
        public bool BlockPostgres { get; set; }
        public bool BlockCatalog { get; set; }
        public string? ServicesBody { get; set; }
        public string PostgresVersionsBody { get; set; } = "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.1\"}]}";
        public string CatalogBody { get; set; } = "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"port\":3306,\"default_port\":3306,\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"},{\"id\":\"9\",\"label\":\"9\"}]},{\"id\":\"redis\",\"name\":\"Redis\",\"port\":6379,\"lines\":[{\"id\":\"7\",\"label\":\"7\"}]}]}";
        public string? CatalogInstallBody { get; set; }
        public string? CatalogCancelBody { get; set; }
        public string? FailPostgresUpdateMajor { get; set; }
        public Queue<string> CatalogBodies { get; } = [];
        public Queue<string> CatalogInstallBodies { get; } = [];
        public List<string> CatalogInstallRequests { get; } = [];
        public TaskCompletionSource PostgresWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleasePostgresWrite { get; } = NewSignal();
        public TaskCompletionSource MySqlWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseMySqlWrite { get; } = NewSignal();
        public TaskCompletionSource RedisWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseRedisWrite { get; } = NewSignal();
        public TaskCompletionSource FirstServiceReadStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseFirstServiceRead { get; } = NewSignal();
        private bool firstServiceRead = true;

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token)
        {
            var path = request.RequestUri!.AbsolutePath;
            Requests.Add($"{request.Method} {path}");
            if (path.EndsWith("/install", StringComparison.Ordinal) && request.Content is { } content)
            {
                CatalogInstallRequests.Add(await content.ReadAsStringAsync(token));
            }
            if (FailPatch && request.Method == HttpMethod.Patch)
            {
                PortChanged = true;
                return Response(HttpStatusCode.Conflict, "port unavailable");
            }
            if (request.Method == HttpMethod.Patch)
            {
                PortChanged = true;
            }
            if (BlockFirstServiceRead && path == "/v1/services" && firstServiceRead)
            {
                firstServiceRead = false;
                FirstServiceReadStarted.TrySetResult();
                await ReleaseFirstServiceRead.Task;
            }
            if (BlockPostgres && path.EndsWith("/install", StringComparison.Ordinal) && path.Contains("/postgres/", StringComparison.Ordinal))
            {
                PostgresWriteStarted.TrySetResult();
                await ReleasePostgresWrite.Task.WaitAsync(token);
            }
            if (BlockCatalog && path.EndsWith("/install", StringComparison.Ordinal) && path.Contains("/catalog/", StringComparison.Ordinal))
            {
                if (path.Contains("/mysql/", StringComparison.Ordinal))
                {
                    MySqlWriteStarted.TrySetResult();
                    await ReleaseMySqlWrite.Task.WaitAsync(token);
                }
                else if (path.Contains("/redis/", StringComparison.Ordinal))
                {
                    RedisWriteStarted.TrySetResult();
                    await ReleaseRedisWrite.Task.WaitAsync(token);
                }
            }
            if (FailPostgresUpdateMajor is { } failedMajor && path == $"/v1/postgres/versions/{failedMajor}/update")
            {
                return Response(HttpStatusCode.Conflict, "update failed");
            }
            return path switch
            {
                "/v1/services" => Response(HttpStatusCode.OK, ServicesBody ?? $"[{{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"category\":\"Database\",\"installed\":false,\"port\":{(PortChanged ? 5433 : 5432)}}} ]"),
                "/v1/postgres/versions" => Response(HttpStatusCode.OK, PostgresVersionsBody),
                "/v1/catalog" => Response(HttpStatusCode.OK, CatalogBodies.Count > 0 ? CatalogBodies.Dequeue() : CatalogBody),
                _ when path.StartsWith("/v1/services/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "[]"),
                _ when path.StartsWith("/v1/postgres/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "{}"),
                _ when CatalogCancelBody is not null && path.EndsWith("/cancel", StringComparison.Ordinal) => Response(HttpStatusCode.OK, CatalogCancelBody),
                _ when (CatalogInstallBodies.Count > 0 || CatalogInstallBody is not null) && path.EndsWith("/install", StringComparison.Ordinal) => Response(HttpStatusCode.OK,
                    CatalogInstallBodies.Count > 0 ? CatalogInstallBodies.Dequeue() : CatalogInstallBody!),
                _ when path.StartsWith("/v1/catalog/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "{\"services\":[]}"),
                _ => Response(HttpStatusCode.NotFound, "missing"),
            };
        }

        private static TaskCompletionSource NewSignal() => new(TaskCreationOptions.RunContinuationsAsynchronously);
        private bool PortChanged { get; set; }

        private static HttpResponseMessage Response(HttpStatusCode status, string body) => new(status)
        {
            Content = new StringContent(status == HttpStatusCode.OK ? body : "{\"error\":\"" + body + "\"}"),
        };
    }
}
