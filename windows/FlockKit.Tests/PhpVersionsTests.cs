namespace Flock.Kit.Tests;

public class PhpVersionsTests
{
    [Fact]
    public void DecodesPhpCatalog()
    {
        var catalog = FlockJson.Decode<PhpCatalog>("""
            {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
             "versions": [
              {"minor":"8.5","latest":"8.5.11","installed":"8.5.11","default":true,"supported":true,
               "status":"installed","update_ready":false,"xdebug":true,"job":null},
              {"minor":"8.4","latest":"8.4.27","installed":"8.4.26","default":false,"supported":true,
               "status":"update","update_ready":true,"xdebug":true,"job":null},
              {"minor":"8.3","latest":"8.3.35","installed":"","default":false,"supported":true,
               "status":"likely","update_ready":false,"xdebug":false,
               "job":{"state":"running","install":true,"auto":false,"step":"Downloading PHP 8.3.35 (12 of 32 MB)","full_version":"",
                      "started_at":"2026-10-13T15:41:00+02:00","finished_at":null,"error":""}},
              {"minor":"7.4","latest":"7.4.33","installed":"","default":false,"supported":false,
               "status":"unsupported","update_ready":false,"xdebug":false,"note":"windows.php.net publishes no Windows build of PHP 7.4.","job":null},
              {"minor":"9.0","latest":"9.0.0","status":"something-new"}
             ]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.Equal(["8.5", "8.4", "8.3", "7.4", "9.0"], catalog.Versions.Select(v => v.Minor));
        Assert.True(catalog.Versions[0].IsDefault && catalog.Versions[0].IsInstalled && catalog.Versions[0].Job is null);
        Assert.True(catalog.Versions[1].Status == PhpStatus.Update && catalog.Versions[1].UpdateReady);
        Assert.True(!catalog.Versions[3].Supported && catalog.Versions[3].Status == PhpStatus.Unsupported);
        Assert.Empty(catalog.Versions[0].Note);
        Assert.True(catalog.Versions[4].Supported);

        var job = catalog.Versions[2].Job!;
        Assert.True(job.State == "running" && job.Install && !job.Auto && job.IsActive);
        Assert.NotNull(job.StartedAt);
        Assert.Null(job.FinishedAt);
        Assert.True(catalog.HasActiveJob);
    }

    [Fact]
    public void DecodesCatalogWithoutCheckAndFinishedJob()
    {
        var catalog = FlockJson.Decode<PhpCatalog>("""
            {"checked_at": null, "check_error": "php.net unreachable",
             "versions": [{"minor":"8.5","latest":"8.5.12","installed":"8.5.11","default":true,"supported":true,
               "status":"failed","update_ready":false,"xdebug":true,
               "job":{"state":"failed","install":true,"auto":true,"step":"","full_version":"8.5.12",
                      "started_at":"2026-10-13T15:41:00Z","finished_at":"2026-10-13T15:47:12.123456789Z","error":"checksum mismatch"}}]}
            """);

        Assert.Null(catalog.CheckedAt);
        Assert.Equal("php.net unreachable", catalog.CheckError);
        Assert.False(catalog.HasActiveJob);
        var job = catalog.Versions[0].Job!;
        Assert.True(job.State == "failed" && job.Auto && job.FinishedAt is not null);
    }
}
