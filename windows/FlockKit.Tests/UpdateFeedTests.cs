using Flock.Kit;

public class UpdateFeedTests
{
    private const string Feed = """
        <?xml version="1.0" encoding="utf-8"?>
        <rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:flock="https://flockphp.com/xml-namespaces/update">
          <channel>
            <title>Flock</title>
            <item>
              <title>Flock 1.1.0-beta.2</title>
              <sparkle:version>1.1.0.2</sparkle:version>
              <sparkle:shortVersionString>1.1.0-beta.2</sparkle:shortVersionString>
              <sparkle:channel>beta</sparkle:channel>
              <sparkle:minimumSystemVersion>10.0.19041</sparkle:minimumSystemVersion>
              <description><![CDATA[<h2>Features</h2><ul><li>Beta things</li></ul>]]></description>
              <enclosure url="https://github.com/flock-php/flock/releases/download/v1.1.0-beta.2/Flock-Setup-1.1.0-beta.2-x64.exe" length="2048" type="application/octet-stream" sparkle:os="windows" flock:sha256="ABCDEF"/>
            </item>
            <item>
              <title>Flock 1.1.0-beta.2</title>
              <sparkle:version>1.1.0.2</sparkle:version>
              <sparkle:shortVersionString>1.1.0-beta.2</sparkle:shortVersionString>
              <sparkle:channel>beta</sparkle:channel>
              <enclosure url="https://github.com/flock-php/flock/releases/download/v1.1.0-beta.2/Flock-1.1.0-beta.2-macOS.dmg" length="4096" type="application/octet-stream" sparkle:os="macos" sparkle:edSignature="sig"/>
            </item>
            <item>
              <title>Flock 1.0.1</title>
              <sparkle:version>1.0.1.1000</sparkle:version>
              <sparkle:shortVersionString>1.0.1</sparkle:shortVersionString>
              <sparkle:minimumSystemVersion>10.0.19041</sparkle:minimumSystemVersion>
              <sparkle:fullReleaseNotesLink>https://github.com/flock-php/flock/releases/tag/v1.0.1</sparkle:fullReleaseNotesLink>
              <description>Fixes</description>
              <enclosure url="https://github.com/flock-php/flock/releases/download/v1.0.1/Flock-Setup-1.0.1-x64.exe" length="1024" type="application/octet-stream" sparkle:os="windows" flock:sha256="0123"/>
            </item>
            <item>
              <title>Flock 1.0.1</title>
              <sparkle:shortVersionString>1.0.1</sparkle:shortVersionString>
              <enclosure url="https://github.com/flock-php/flock/releases/download/v1.0.1/Flock-1.0.1-macOS.dmg" length="1024"/>
            </item>
            <item>
              <title>Not a release</title>
              <sparkle:shortVersionString>dev-abc123</sparkle:shortVersionString>
              <enclosure url="https://example.com/x.exe" sparkle:os="windows"/>
            </item>
          </channel>
        </rss>
        """;

    private static readonly Version Windows11 = new(10, 0, 26100);

    [Fact]
    public void ParsesTheItems()
    {
        var items = UpdateFeed.Parse(Feed);

        Assert.Equal(4, items.Count);
        var beta = items[0];
        Assert.Equal(new AppVersion(1, 1, 0, 2), beta.Version);
        Assert.Equal(("beta", "windows", 2048L, "abcdef"), (beta.Channel, beta.Os, beta.Length, beta.Sha256));
        Assert.Equal(new Version(10, 0, 19041), beta.MinimumSystemVersion);
        Assert.Contains("Beta things", beta.Notes);
        Assert.Null(beta.NotesUrl);

        var stable = items[2];
        Assert.Equal(("", "windows"), (stable.Channel, stable.Os));
        Assert.Equal("https://github.com/flock-php/flock/releases/tag/v1.0.1", stable.NotesUrl);
        // Without sparkle:os an enclosure is for macOS.
        Assert.Equal("macos", items[3].Os);
        Assert.Null(items[3].MinimumSystemVersion);
    }

    [Fact]
    public void RejectsAFeedThatIsNotXml() =>
        Assert.Throws<FormatException>(() => UpdateFeed.Parse("<html><body>Not found"));

    [Fact]
    public void StableUsersIgnoreBetas()
    {
        var update = UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.0.0"), beta: false, Windows11);
        Assert.Equal("1.0.1", update?.Version.ToString());
        Assert.Equal("windows", update?.Os);
    }

    [Fact]
    public void BetaUsersGetTheNewestBeta()
    {
        var update = UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.0.1"), beta: true, Windows11);
        Assert.Equal("1.1.0-beta.2", update?.Version.ToString());
        Assert.Null(UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.1.0-beta.2"), beta: true, Windows11));
    }

    [Fact]
    public void BetaUsersGetTheReleaseOnceItIsNewerThanTheirBeta()
    {
        var items = UpdateFeed.Parse(Feed);
        items.Add(items[2] with { Version = new AppVersion(1, 1, 0) });

        Assert.Equal("1.1.0", UpdateFeed.Pick(items, AppVersion.Parse("1.1.0-beta.2"), beta: true, Windows11)?.Version.ToString());
        // A stable user on a beta (the switch was turned off) also moves on once the release is out.
        Assert.Equal("1.1.0", UpdateFeed.Pick(items, AppVersion.Parse("1.1.0-beta.2"), beta: false, Windows11)?.Version.ToString());
    }

    [Fact]
    public void BetasGetTheNextBetaOnAnyChannel() =>
        // Back on stable while running a beta: it keeps getting betas until the release is out.
        Assert.Equal("1.1.0-beta.2", UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.1.0-beta.1"), beta: false, Windows11)?.Version.ToString());

    [Fact]
    public void NeverOffersAnOlderVersion()
    {
        // Back on stable while running the newest beta: 1.0.1 is older, so there is nothing to install.
        Assert.Null(UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.1.0-beta.2"), beta: false, Windows11));
        Assert.Null(UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.Parse("1.0.1"), beta: false, Windows11));
    }

    [Fact]
    public void SkipsReleasesForANewerWindows()
    {
        var items = UpdateFeed.Parse(Feed);
        items.Add(items[2] with { Version = new AppVersion(2, 0, 0), MinimumSystemVersion = new Version(10, 0, 30000) });

        Assert.Equal("1.0.1", UpdateFeed.Pick(items, AppVersion.Parse("1.0.0"), beta: false, Windows11)?.Version.ToString());
        Assert.Equal("2.0.0", UpdateFeed.Pick(items, AppVersion.Parse("1.0.0"), beta: false, new Version(10, 0, 30000))?.Version.ToString());
    }

    [Fact]
    public void DevelopmentBuildsGetNoUpdates() =>
        Assert.Null(UpdateFeed.Pick(UpdateFeed.Parse(Feed), AppVersion.FromInformational("dev"), beta: true, Windows11));

    [Theory]
    [InlineData("stable", false)]
    [InlineData("beta", true)]
    [InlineData("experimental", true)]
    [InlineData(null, false)]
    public void BetaAndExperimentalChannelsTakeBetas(string? channel, bool betas) =>
        Assert.Equal(betas, UpdateFeed.TakesBetas(channel));
}

public class AppVersionTests
{
    [Theory]
    [InlineData("1.0.0", "1.0.0")]
    [InlineData("1.1.0-beta.2", "1.1.0-beta.2")]
    [InlineData("1.1.0+3f2a9c1", "1.1.0")]
    [InlineData("12.0.10-beta.11+abc", "12.0.10-beta.11")]
    public void ParsesReleasedVersions(string informational, string expected) =>
        Assert.Equal(expected, AppVersion.FromInformational(informational)?.ToString());

    [Theory]
    [InlineData(null)]
    [InlineData("dev")]
    [InlineData("dev-3f2a9c1")]
    [InlineData("v1.0.0")]
    [InlineData("1.0.0-3-g3f2a9c1")]
    [InlineData("1.0.0-dirty")]
    [InlineData("1.0.0-rc.1")]
    [InlineData("1.0")]
    public void DevelopmentBuildsAreNotReleases(string? text) => Assert.Null(AppVersion.FromInformational(text));

    [Fact]
    public void BetasSortBetweenReleases()
    {
        string[] ordered = ["1.0.0", "1.0.1", "1.1.0-beta.1", "1.1.0-beta.2", "1.1.0-beta.10", "1.1.0", "1.10.0", "2.0.0-beta.1", "2.0.0"];
        var versions = ordered.Select(v => AppVersion.Parse(v)!).ToList();

        Assert.Equal(ordered, Enumerable.Reverse(versions).Order().Select(v => v.ToString()));
        Assert.True(AppVersion.Parse("1.1.0")! > AppVersion.Parse("1.1.0-beta.10")!);
        Assert.True(AppVersion.Parse("1.1.0-beta.1")! > AppVersion.Parse("1.0.9")!);
        Assert.Equal(0, AppVersion.Parse("1.1.0")!.CompareTo(AppVersion.Parse("1.1.0")));
    }

    [Fact]
    public void StableSortsAboveTheLargestBetaNumber()
    {
        Assert.True(AppVersion.Parse("1.1.0")! > AppVersion.Parse("1.1.0-beta.2147483647")!);
    }
}

public class UpdateLinkTests
{
    [Theory]
    [InlineData("flock://update", true)]
    [InlineData("FLOCK://UPDATE/", true)]
    [InlineData("flock://update/now", false)]
    [InlineData("https://update", false)]
    [InlineData("flock://updates", false)]
    [InlineData(null, false)]
    public void RecognizesUpdateLinks(string? text, bool update)
    {
        Assert.Equal(update, UpdateLink.IsUpdateLink(text));
        Assert.Equal(update, SecureLink.IsFlockLink(text));
    }
}