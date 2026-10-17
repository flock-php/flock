using Flock.Kit;

public class ServiceVersionsTests
{
    [Fact]
    public void DecodesPostgresVersions()
    {
        var versions = FlockJson.Decode<PostgresVersions>("""
            {"active":"18","running":true,"enabled":true,"port":5432,"checked_at":"2026-10-14T08:00:00Z","check_error":"",
             "majors":[
              {"major":"18","installed":"18.6","latest":"18.7","update":true,"prerelease":false,"yanked":"crashes on start","active":true,"has_data":true,"data_size":48234496,"eol":"2030-11-14","busy":""},
              {"major":"17","installed":"","latest":"17.6","update":false,"prerelease":true,"active":false,"has_data":true,"data_size":1024,"eol":"2029-11-08","busy":"installing"},
              {"major":"16","installed":"","latest":"","update":false,"active":false,"has_data":false,"data_size":0,"eol":"","busy":""}]}
            """);

        Assert.Equal(("18", true, true, 5432), (versions.Active, versions.Running, versions.Enabled, versions.Port));
        Assert.NotNull(versions.CheckedAt);
        Assert.Equal(["18", "17", "16"], versions.Majors.Select(m => m.Major));
        Assert.Same(versions.Majors[0], versions.ActiveMajor);
        Assert.True(versions.AnyInstalled);
        Assert.True(versions.AnyBusy);
        Assert.True(versions.AnyUpdate);

        var current = versions.Majors[0];
        Assert.True(current.IsInstalled);
        Assert.Equal(48234496, current.DataSize);
        Assert.Equal("crashes on start", current.Yanked);
        Assert.False(current.Prerelease);
        Assert.True(versions.Majors[1].Prerelease);
        Assert.Equal(new DateOnly(2030, 11, 14), current.EolDate);
        Assert.False(current.IsBusy);
        Assert.Equal("", current.BusyText);

        var installing = versions.Majors[1];
        Assert.False(installing.IsInstalled);
        Assert.True(installing.HasData);
        Assert.Equal("Installing…", installing.BusyText);

        Assert.Null(versions.Majors[2].EolDate);
    }

    [Fact]
    public void DecodesPostgresVersionsBeforeTheFirstCheck()
    {
        var versions = FlockJson.Decode<PostgresVersions>("""
            {"active":"","running":false,"enabled":true,"port":5432,"checked_at":null,"check_error":"Get \"https://flockphp.com/binaries/manifest.json\": dial tcp: timeout",
             "majors":[{"major":"18","installed":"","latest":"","update":false,"active":false,"has_data":false,"data_size":0,"eol":"2030-11-14","busy":"vacuuming"}]}
            """);

        Assert.Null(versions.CheckedAt);
        Assert.Contains("timeout", versions.CheckError);
        Assert.Null(versions.ActiveMajor);
        Assert.False(versions.AnyInstalled);
        // An operation a newer daemon knows about still counts as busy, with a generic text.
        Assert.True(versions.AnyBusy);
        Assert.Equal("Working…", versions.Majors[0].BusyText);

        Assert.Empty(FlockJson.Decode<PostgresVersions>("""{"active":"","majors":null}""").Majors);
    }

    [Fact]
    public void TellsHowCloseAMajorIsToItsEndOfLife()
    {
        var major = new PostgresMajor { Eol = "2026-11-12" };
        Assert.Equal(EndOfLife.Supported, major.EndOfLifeState(new DateOnly(2025, 10, 14)));
        Assert.Equal(EndOfLife.Soon, major.EndOfLifeState(new DateOnly(2026, 10, 14)));
        Assert.Equal(EndOfLife.Past, major.EndOfLifeState(new DateOnly(2026, 11, 12)));
        Assert.Equal(EndOfLife.Unknown, new PostgresMajor { Eol = "" }.EndOfLifeState(new DateOnly(2026, 10, 14)));
        Assert.Equal(EndOfLife.Unknown, new PostgresMajor { Eol = "soon" }.EndOfLifeState(new DateOnly(2026, 10, 14)));
    }

    [Fact]
    public void DecodesComponentsWithoutJobs()
    {
        var catalog = FlockJson.Decode<ComponentCatalog>("""
            {"checked_at":null,"check_error":"",
             "components":[
              {"id":"valkey","name":"Valkey","description":"","installed":"8.1.3","latest":"8.1.4","update":true,"method":"download","estimate":"5 MB download","job":null},
              {"id":"other","name":"Other tool","description":"does other things",
               "installed":"","latest":"1.6","update":false,"method":"download","estimate":"330 MB download, 12 MB kept"}]}
            """);

        Assert.Null(catalog.CheckedAt);
        Assert.False(catalog.HasRunningJob);
        var redis = catalog[ComponentId.Valkey]!;
        Assert.True(redis.IsInstalled);
        Assert.True(redis.Update);
        Assert.Equal(ComponentMethod.Download, redis.Method);
        Assert.Null(redis.Job);
        var client = catalog["other"]!;
        Assert.False(client.IsInstalled);
        Assert.Equal("330 MB download, 12 MB kept", client.Estimate);
        Assert.Null(client.Job);
        Assert.Null(catalog["memcached"]);
    }

    [Fact]
    public void DecodesRunningAndFailedJobs()
    {
        var catalog = FlockJson.Decode<ComponentCatalog>("""
            {"checked_at":"2026-10-14T08:00:00.123456789Z","check_error":"",
             "components":[
              {"id":"valkey","name":"Valkey","installed":"","latest":"8.1.4","update":false,"method":"download","estimate":"",
               "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"2026-10-14T08:01:00Z"}},
              {"id":"other","name":"Other tool","installed":"1.5","latest":"1.6","update":true,"method":"build","estimate":"about 5 minutes",
               "job":{"action":"update","state":"failed","phase":"building","progress":-1,"error":"make: *** [all] Error 2","started_at":"2026-10-14T08:01:00Z"}}]}
            """);

        Assert.True(catalog.HasRunningJob);
        var running = catalog[ComponentId.Valkey]!.Job!;
        Assert.True(running.IsRunning);
        Assert.False(running.IsFailed);
        Assert.True(running.HasProgress);
        Assert.Equal(0.42, running.Progress);
        Assert.Equal("Downloading…", running.PhaseText);
        Assert.NotNull(running.StartedAt);

        var failed = catalog["other"]!.Job!;
        Assert.True(failed.IsFailed);
        Assert.False(failed.IsRunning);
        Assert.False(failed.HasProgress);
        Assert.Equal("make: *** [all] Error 2", failed.Error);
    }

    [Fact]
    public void FallsBackForJobStatesAndPhasesItDoesNotKnow()
    {
        var job = FlockJson.Decode<ComponentJob>("""{"action":"update","state":"paused","phase":"verifying"}""");
        Assert.False(job.IsRunning);
        Assert.False(job.IsFailed);
        Assert.Equal("Updating…", job.PhaseText);
        // Without a progress field the job is indeterminate.
        Assert.False(job.HasProgress);
        Assert.Equal("Working…", FlockJson.Decode<ComponentJob>("""{"action":"install","state":"running","phase":""}""").PhaseText);
    }

    [Fact]
    public void DecodesTheServiceCatalog()
    {
        var catalog = FlockJson.Decode<ServiceCatalog>("""
            {"checked_at":"2026-10-14T08:00:00Z","check_error":"",
             "services":[
              {"id":"mysql","name":"MySQL","category":"Database","description":"The most popular database","available":true,
               "method":"download","estimate":"250 MB download","installed":"8.4.6","line":"8.4","latest":"8.4.7","update":true,
               "port":3306,"default_port":3306,
               "lines":[{"id":"9.4","label":"9.4 Innovation","latest":"9.4.0","installed":false,"has_data":false},
                        {"id":"8.4","label":"8.4 LTS","latest":"8.4.7","installed":true,"has_data":true}],"job":null},
              {"id":"redis","name":"Redis","category":"Cache & Queue","available":true,"method":"download","installed":"","port":6380,"default_port":6379,
               "lines":[{"id":"8.2"}],
               "job":{"action":"install","state":"failed","phase":"downloading","progress":0.3,"error":"503","started_at":"2026-10-14T08:01:00Z","version":"8.2.1","line":"8.2"}},
              {"id":"typesense","name":"Typesense","category":"Search","available":false,"unavailable":"Typesense has no Windows build.","method":"","lines":null}]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.False(catalog.HasRunningJob);
        var mysql = catalog["mysql"]!;
        Assert.True(mysql.IsInstalled);
        Assert.True(mysql.Update);
        Assert.Equal("8.4", mysql.PreferredLine!.Id);
        Assert.Equal(["9.4 Innovation", "8.4 LTS"], mysql.Lines.Select(l => l.Label));
        Assert.True(mysql.Lines[1].HasData);

        var redis = catalog["redis"]!;
        Assert.False(redis.IsInstalled);
        Assert.Equal(6380, redis.Port);
        Assert.Equal(6379, redis.DefaultPort);
        Assert.Equal("8.2", redis.Lines[0].Label);
        Assert.True(redis.Job!.IsFailed);
        Assert.Equal("8.2.1", redis.Job.Version);
        Assert.Equal(0.3, redis.Job.Progress);

        var typesense = catalog["typesense"]!;
        Assert.False(typesense.Available);
        Assert.StartsWith("Typesense", typesense.Unavailable);
        Assert.Empty(typesense.Lines);
        Assert.Null(typesense.PreferredLine);
        Assert.Null(catalog["memcached"]);
    }
}
