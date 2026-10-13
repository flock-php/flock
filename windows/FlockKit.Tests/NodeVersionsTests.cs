using Flock.Kit;

public class NodeVersionsTests
{
    [Fact]
    public void DecodesNodeCatalog()
    {
        var catalog = FlockJson.Decode<NodeCatalog>("""
            {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
             "versions": [
              {"major":"24","latest":"24.11.0","codename":"Krypton","support":"lts","end_of_life":"2028-04-30",
               "installed":"","default":false,"update":false},
              {"major":"22","latest":"22.21.0","codename":"Jod","support":"maintenance","end_of_life":"2027-04-30",
               "installed":"22.20.0","default":true,"update":true,"npm":"10.9.2","npm_latest":"12.2.0"},
              {"major":"14","latest":"14.21.3","codename":"","support":"","end_of_life":"",
               "installed":"14.21.3","default":false,"update":false}]}
            """);

        Assert.NotNull(catalog.CheckedAt);
        Assert.Equal(["24", "22", "14"], catalog.Versions.Select(v => v.Major));

        var lts = catalog.Versions[0];
        Assert.Equal(("Krypton", NodeSupport.Lts, "2028-04-30"), (lts.Codename, lts.Support, lts.EndOfLife));
        Assert.False(lts.IsInstalled);

        var current = catalog.Versions[1];
        Assert.True(current.IsDefault);
        Assert.True(current.Update);
        Assert.Equal("22.20.0", current.Installed);
        Assert.Equal(("10.9.2", "12.2.0"), (current.Npm, current.NpmLatest));
        Assert.Equal("", lts.Npm);

        Assert.Equal("", catalog.Versions[2].Support);
    }

    [Fact]
    public void DecodesCatalogWhenNodejsOrgIsUnreachable()
    {
        var catalog = FlockJson.Decode<NodeCatalog>("""{"checked_at": null, "check_error": "dial tcp: timeout", "versions": []}""");
        Assert.Null(catalog.CheckedAt);
        Assert.Equal("dial tcp: timeout", catalog.CheckError);
        Assert.Empty(catalog.Versions);
    }
}
