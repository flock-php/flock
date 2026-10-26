using Flock.Kit;

public class InstallLinkTests
{
    [Fact]
    public void ParsesInstallLinks()
    {
        Assert.Equal(new InstallLink("mysql", "8.4"), InstallLink.Parse("flock://install/mysql/8.4"));
        Assert.Equal(new InstallLink("postgres", "18"), InstallLink.Parse("flock://install/postgres/18"));
        Assert.Equal(new InstallLink("meilisearch"), InstallLink.Parse("flock://install/meilisearch"));
        Assert.Equal(new InstallLink("mysql", "8.4"), InstallLink.Parse("FLOCK://INSTALL/MySQL/8.4/"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("--background")]
    [InlineData("https://install/mysql/8.4")]
    [InlineData("flock://uninstall/mysql")]
    [InlineData("flock://install")]
    [InlineData("flock://install/mysql/8.4/extra")]
    [InlineData("flock://install/my%20sql")]
    [InlineData("flock://install/mysql/8.4%0A")]
    [InlineData("flock://install/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")]
    public void RejectsAnythingElse(string? text)
    {
        Assert.Null(InstallLink.Parse(text));
    }

    private static (List<Service>, ServiceCatalog, PostgresVersions) Fixtures() => (
        FlockJson.Decode<List<Service>>("""
            [{"id":"postgres","name":"PostgreSQL","available":true},
             {"id":"mysql","name":"MySQL","available":true},
             {"id":"redis","name":"Redis","available":true},
             {"id":"mariadb","name":"MariaDB","available":true},
             {"id":"typesense","name":"Typesense","available":false,"unavailable":"Typesense has no Windows build."}]
            """),
        FlockJson.Decode<ServiceCatalog>("""
            {"services":[
              {"id":"mysql","name":"MySQL","installed":"","lines":[
                {"id":"9.7","label":"9.7 LTS","latest":"9.7.1"},{"id":"8.4","label":"8.4 LTS","latest":"8.4.7"}]},
              {"id":"redis","name":"Redis","installed":"8.4.1","line":"8.4","lines":[
                {"id":"8.10","label":"8.10"},{"id":"8.4","label":"8.4","installed":true}]}]}
            """),
        FlockJson.Decode<PostgresVersions>("""
            {"majors":[{"major":"18","latest":"18.7.0"},{"major":"17","installed":"17.4.0"}]}
            """));

    [Fact]
    public void ResolvesAgainstWhatFlockOffers()
    {
        var (services, catalog, postgres) = Fixtures();
        InstallLinkResolution? Resolve(string service, string? line = null) => new InstallLink(service, line).Resolve(services, catalog, postgres);

        Assert.Equal(new InstallLinkTarget("mysql", "MySQL", "8.4", "8.4 LTS", "8.4.7"), Resolve("mysql", "8.4")?.Target);
        Assert.Equal("Install MySQL 8.4 LTS?", Resolve("mysql", "8.4")?.Title);
        Assert.Equal("9.7", Resolve("mysql")?.Target?.Line);
        Assert.Equal(new InstallLinkTarget("postgres", "PostgreSQL", "18", "18", "18.7.0"), Resolve("postgres")?.Target);

        Assert.Equal(InstallLinkResolution.AlreadyInstalled("PostgreSQL 17"), Resolve("postgres", "17"));
        Assert.Equal(InstallLinkResolution.AlreadyInstalled("Redis 8.4"), Resolve("redis", "8.4"));
        // Another line installs next to the installed one.
        Assert.Equal(new InstallLinkTarget("redis", "Redis", "8.10", "8.10", ""), Resolve("redis", "8.10")?.Target);
        Assert.Equal(InstallLinkResolution.Unavailable("Typesense", "Typesense has no Windows build."), Resolve("typesense"));
        Assert.Equal(InstallLinkResolution.UnknownVersion("MySQL", "5.7"), Resolve("mysql", "5.7"));
        Assert.Equal(InstallLinkResolution.UnknownVersion("PostgreSQL", "9"), Resolve("postgres", "9"));
        Assert.Equal(InstallLinkResolution.Unknown("oracle"), Resolve("oracle"));
        // In the services list, but this daemon's catalog doesn't know it.
        Assert.Equal(InstallLinkResolution.Unknown("MariaDB"), Resolve("mariadb"));
    }

    [Fact]
    public void WaitsForTheVersionsBeforeResolving()
    {
        var (services, catalog, postgres) = Fixtures();
        Assert.Null(new InstallLink("mysql").Resolve([], catalog, postgres));
        Assert.Null(new InstallLink("mysql").Resolve(services, null, postgres));
        Assert.Null(new InstallLink("postgres").Resolve(services, catalog, null));
    }

    [Fact]
    public void ParsesPhpAndNodeLinks()
    {
        Assert.Equal(new InstallLink(InstallLink.Php, "8.5"), InstallLink.Parse("flock://install/php/8.5"));
        Assert.Equal(new InstallLink(InstallLink.Node, "24"), InstallLink.Parse("flock://install/node/24"));
        Assert.Equal(new InstallLink(InstallLink.Php), InstallLink.Parse("flock://install/php"));
    }

    [Fact]
    public void ResolvesPhpLinksAgainstThePhpVersions()
    {
        var catalog = FlockJson.Decode<PhpCatalog>("""
            {"versions":[
              {"minor":"8.6","latest":"8.6.0RC2","status":"available","prerelease":true},
              {"minor":"8.10","latest":"8.10.1","status":"available"},
              {"minor":"8.5","latest":"8.5.12","status":"available"},
              {"minor":"8.4","latest":"8.4.26","installed":"8.4.26","status":"installed","default":true},
              {"minor":"8.3","latest":"8.3.20","status":"available","job":{"state":"running","phase":"downloading"}},
              {"minor":"7.3","installed":"7.3.33","status":"installed"}]}
            """);
        InstallLinkResolution? Resolve(string? line = null) => new InstallLink(InstallLink.Php, line).ResolvePhp(catalog);

        Assert.Equal(new InstallLinkTarget("php", "PHP", "8.5", "8.5", "8.5.12"), Resolve("8.5")?.Target);
        Assert.Equal("Install PHP 8.5?", Resolve("8.5")?.Title);
        Assert.Equal("A link asked Flock to install PHP 8.5.12. Sites that use it are served as soon as it's installed.", Resolve("8.5")?.Message);
        // The newest that isn't a pre-release, by number.
        Assert.Equal("8.10", Resolve()?.Target?.Line);
        // A pre-release asked for by name is offered.
        Assert.Equal("8.6", Resolve("8.6")?.Target?.Line);

        Assert.Equal(InstallLinkResolution.AlreadyInstalled("PHP 8.4", "PHP"), Resolve("8.4"));
        Assert.Equal("You'll find it in Settings → PHP.", Resolve("8.4")?.Message);
        Assert.Equal(InstallLinkResolution.AlreadyInstalled("PHP 7.3", "PHP"), Resolve("7.3"));
        Assert.Equal(InstallLinkResolution.Installing("PHP 8.3", "PHP"), Resolve("8.3"));
        Assert.Equal("Flock doesn't offer PHP 7.2", Resolve("7.2")?.Title);
        Assert.Null(Resolve("7.2")?.Target);

        Assert.Null(new InstallLink(InstallLink.Php, "8.5").ResolvePhp(null));
        Assert.Equal("Flock doesn't offer PHP", new InstallLink(InstallLink.Php).ResolvePhp(new PhpCatalog())?.Title);
    }

    [Fact]
    public void ResolvesNodeLinksAgainstTheNodeLines()
    {
        var catalog = FlockJson.Decode<NodeCatalog>("""
            {"versions":[
              {"major":"25","latest":"25.1.0"},
              {"major":"24","latest":"24.11.1"},
              {"major":"22","latest":"22.21.0","installed":"22.21.0","default":true},
              {"major":"9","latest":"9.11.2"}]}
            """);
        InstallLinkResolution? Resolve(string? line = null, IReadOnlyCollection<string>? installing = null) =>
            new InstallLink(InstallLink.Node, line).ResolveNode(catalog, installing);

        Assert.Equal(new InstallLinkTarget("node", "Node.js", "24", "24", "24.11.1"), Resolve("24")?.Target);
        Assert.Equal("Install Node.js 24?", Resolve("24")?.Title);
        Assert.Equal("25", Resolve()?.Target?.Line);
        Assert.Equal(InstallLinkResolution.AlreadyInstalled("Node.js 22", "Node"), Resolve("22"));
        Assert.Equal(InstallLinkResolution.Installing("Node.js 24", "Node"), Resolve("24", ["24"]));
        Assert.Equal("Flock doesn't offer Node.js 12", Resolve("12")?.Title);
        Assert.Null(new InstallLink(InstallLink.Node, "24").ResolveNode(null));
    }
}