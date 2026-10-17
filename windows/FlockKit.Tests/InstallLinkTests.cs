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
}
