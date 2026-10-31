using Flock.Kit;

public class SecureLinkTests
{
    [Fact]
    public void ParsesSecureLinks()
    {
        Assert.Equal(new SecureLink("watchtower"), SecureLink.Parse("flock://secure/watchtower"));
        Assert.Equal(new SecureLink("my-app"), SecureLink.Parse("FLOCK://SECURE/My-App/"));
        Assert.True(SecureLink.IsFlockLink("flock://secure/watchtower"));
        Assert.True(SecureLink.IsFlockLink("flock://install/mysql"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("--background")]
    [InlineData("https://secure/watchtower")]
    [InlineData("flock://unsecure/watchtower")]
    [InlineData("flock://secure")]
    [InlineData("flock://secure/watchtower/extra")]
    [InlineData("flock://secure/watch%20tower")]
    [InlineData("flock://secure/watchtower%0A")]
    [InlineData("flock://secure/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")]
    public void RejectsAnythingElse(string? text)
    {
        Assert.Null(SecureLink.Parse(text));
        Assert.False(SecureLink.IsFlockLink(text));
    }

    [Fact]
    public void ResolvesAgainstTheLinkedSites()
    {
        var sites = FlockJson.Decode<Status>("""
            {"version":"1","sites":[
              {"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower"},
              {"name":"safe","domain":"safe.test","path":"/code/safe","secured":true}]}
            """).Sites;

        Assert.Null(new SecureLink("watchtower").Resolve(null));

        var secure = new SecureLink("watchtower").Resolve(sites)!;
        Assert.Equal("watchtower", secure.Site);
        Assert.Equal("Serve watchtower over HTTPS?", secure.Title);
        Assert.Contains("watchtower.test", secure.Message);

        var secured = new SecureLink("safe").Resolve(sites)!;
        Assert.Null(secured.Site);
        Assert.Equal("safe is already secured", secured.Title);

        var unknown = new SecureLink("nope").Resolve(sites)!;
        Assert.Null(unknown.Site);
        Assert.Equal("Flock has no site called nope", unknown.Title);
    }
}