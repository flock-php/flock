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
    public void DecodesRunningAndFailedJobs()
    {
        var running = FlockJson.Decode<ComponentJob>("""
            {"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"2026-10-14T08:01:00Z"}
            """);
        Assert.True(running.IsRunning);
        Assert.False(running.IsFailed);
        Assert.True(running.HasProgress);
        Assert.Equal(0.42, running.Progress);
        Assert.Equal("Downloading…", running.PhaseText);
        Assert.NotNull(running.StartedAt);

        var failed = FlockJson.Decode<ComponentJob>("""
            {"action":"update","state":"failed","phase":"installing","progress":-1,"error":"checksum mismatch","started_at":"2026-10-14T08:01:00Z"}
            """);
        Assert.True(failed.IsFailed);
        Assert.False(failed.IsRunning);
        Assert.False(failed.HasProgress);
        Assert.Equal("checksum mismatch", failed.Error);
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
    public void CatalogJobsShareProgressBoundariesAndKeepCatalogFields()
    {
        var job = FlockJson.Decode<CatalogJob>("""
            {"action":"install","state":"running","phase":"verifying","progress":1,"version":"8.2.1","line":"8.2"}
            """);

        Assert.True(job.IsRunning);
        Assert.True(job.HasProgress);
        Assert.Equal("Working…", job.PhaseText);
        Assert.Equal(("8.2.1", "8.2"), (job.Version, job.Line));
        Assert.False(FlockJson.Decode<CatalogJob>("""{"progress":1.1}""").HasProgress);
    }

    [Fact]
    public void DecodesTheServiceCatalog()
    {
        var catalog = FlockJson.Decode<ServiceCatalog>("""
            {"checked_at":"2026-10-14T08:00:00Z","check_error":"",
             "services":[
              {"id":"mysql","name":"MySQL","category":"Database","description":"The most popular database","available":true,
               "estimate":"250 MB download","installed":"8.4.6","line":"8.4","latest":"8.4.7","update":true,
               "port":3306,"default_port":3306,
               "lines":[{"id":"9.4","label":"9.4 Innovation","latest":"9.4.1","installed":true,"version":"9.4.0","update":true,"yanked":"crashes","has_data":false},
                        {"id":"8.4","label":"8.4 LTS","latest":"8.4.7","installed":true,"version":"8.4.6","active":true,"update":true,"has_data":true}],"job":null},
              {"id":"redis","name":"Redis","category":"Cache & Queue","available":true,"installed":"","port":6380,"default_port":6379,
               "lines":[{"id":"8.2"}],
               "job":{"action":"install","state":"failed","phase":"downloading","progress":0.3,"error":"503","started_at":"2026-10-14T08:01:00Z","version":"8.2.1","line":"8.2"}},
              {"id":"typesense","name":"Typesense","category":"Search","available":false,"unavailable":"Typesense has no Windows build.","lines":null}]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.False(catalog.HasRunningJob);
        var mysql = catalog["mysql"]!;
        Assert.True(mysql.IsInstalled);
        Assert.True(mysql.Update);
        Assert.Equal("8.4", mysql.PreferredLine!.Id);
        Assert.Equal(["9.4 Innovation", "8.4 LTS"], mysql.Lines.Select(l => l.Label));
        Assert.True(mysql.Lines[1].HasData);
        // Lines install side by side; the active one runs.
        Assert.Equal([true, true], mysql.Lines.Select(l => l.Installed));
        Assert.Equal([false, true], mysql.Lines.Select(l => l.Active));
        Assert.Equal("9.4.0", mysql.Lines[0].Version);
        Assert.True(mysql.Lines[0].Update);
        Assert.Equal("crashes", mysql.Lines[0].Yanked);

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