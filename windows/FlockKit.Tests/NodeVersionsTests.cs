using Flock.Kit;

public class NodeVersionsTests
{
    [Fact]
    public void DecodesNodeCatalog()
    {
        var catalog = FlockJson.Decode<NodeCatalog>("""
            {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
             "versions": [
              {"major":"24","latest":"24.11.0",
               "installed":"","default":false,"update":false},
              {"major":"22","latest":"22.21.0",
               "installed":"22.20.0","default":true,"update":true,"npm":"10.9.2","npm_latest":"12.2.0"},
              {"major":"14","latest":"14.21.3",
               "installed":"14.21.3","default":false,"update":false}]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.Equal(["24", "22", "14"], catalog.Versions.Select(v => v.Major));

        var newest = catalog.Versions[0];
        Assert.False(newest.IsInstalled);

        var current = catalog.Versions[1];
        Assert.True(current.IsDefault);
        Assert.True(current.Update);
        Assert.Equal("22.20.0", current.Installed);
        Assert.Equal(("10.9.2", "12.2.0"), (current.Npm, current.NpmLatest));
        Assert.Equal("", newest.Npm);

        Assert.True(catalog.Versions[2].IsInstalled);
    }

    [Fact]
    public void DecodesCatalogWhenTheManifestIsUnreachable()
    {
        var catalog = FlockJson.Decode<NodeCatalog>("""{"checked_at": null, "check_error": "dial tcp: timeout", "versions": []}""");
        Assert.Null(catalog.CheckedAt);
        Assert.Equal("dial tcp: timeout", catalog.CheckError);
        Assert.Empty(catalog.Versions);
    }
}