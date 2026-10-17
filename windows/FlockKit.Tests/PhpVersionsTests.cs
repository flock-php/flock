namespace Flock.Kit.Tests;

public class PhpVersionsTests
{
    [Fact]
    public void DecodesPhpCatalog()
    {
        var catalog = FlockJson.Decode<PhpCatalog>("""
            {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
             "versions": [
              {"minor":"8.5","latest":"8.5.11","installed":"8.5.11","status":"installed","update":false,"size":34000000,
               "prerelease":false,"yanked":"","default":true,"xdebug":true,"job":null},
              {"minor":"8.4","latest":"8.4.27","installed":"8.4.26","status":"update","update":true,"size":33000000,
               "prerelease":false,"yanked":"crashes on start","default":false,"xdebug":true,"job":null},
              {"minor":"8.3","latest":"8.3.35","installed":"","status":"available","update":false,"size":32000000,
               "prerelease":false,"yanked":"","default":false,"xdebug":false,
               "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"",
                      "started_at":"2026-10-13T15:41:00+02:00"}},
              {"minor":"8.6","latest":"8.6.0RC2","installed":"","status":"available","prerelease":true},
              {"minor":"9.0","latest":"9.0.0","status":"something-new"}
             ]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.Equal(["8.5", "8.4", "8.3", "8.6", "9.0"], catalog.Versions.Select(v => v.Minor));
        Assert.True(catalog.Versions[0].IsDefault && catalog.Versions[0].IsInstalled && catalog.Versions[0].Job is null && catalog.Versions[0].Size == 34000000);
        Assert.True(catalog.Versions[1].Status == PhpStatus.Update && catalog.Versions[1].Update);
        Assert.Equal("crashes on start", catalog.Versions[1].Yanked);
        Assert.True(catalog.Versions[3].Prerelease && !catalog.Versions[3].Update);
        Assert.Equal("something-new", catalog.Versions[4].Status);

        var job = catalog.Versions[2].Job!;
        Assert.True(job.IsRunning && job.Phase == "downloading" && job.HasProgress && job.Progress == 0.42);
        Assert.True(catalog.HasActiveJob);
    }

    [Fact]
    public void DecodesCatalogWithoutCheckAndFailedJob()
    {
        var catalog = FlockJson.Decode<PhpCatalog>("""
            {"checked_at": null, "check_error": "the package manifest's signature doesn't verify",
             "versions": [{"minor":"8.5","latest":"8.5.12","installed":"8.5.11","status":"update","update":true,"default":true,
               "job":{"action":"update","state":"failed","phase":"downloading","progress":-1,"error":"checksum mismatch",
                      "started_at":"2026-10-13T15:41:00Z"}}]}
            """);

        Assert.Null(catalog.CheckedAt);
        Assert.Contains("signature", catalog.CheckError);
        Assert.False(catalog.HasActiveJob);
        var job = catalog.Versions[0].Job!;
        Assert.True(job.IsFailed && job.Action == "update" && job.Error == "checksum mismatch");
    }
}
