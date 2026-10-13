namespace Flock.Kit.Tests;

public class SettingsTests
{
    [Fact]
    public void DecodesConfig()
    {
        var config = FlockJson.Decode<FlockConfig>("""
            {"tld":"test","default_php":"8.5","default_node":"22","xdebug":false,"dumps":true,
             "root":"C:\\Users\\x\\AppData\\Roaming\\Flock",
             "ports":{"http":80,"https":443,"dns":53535,"smtp":2525,"postgres":5432,"redis":6379,"dumps":9912,"api":53531},
             "parked":["C:\\Users\\x\\Developer"],
             "php":[{"version":"8.5","full_version":"8.5.11","ini":"/f/php/8.5/php.ini","xdebug_available":true},
                    {"version":"8.4","full_version":"8.4.26","ini":"/f/php/8.4/php.ini","xdebug_available":false}],
             "node":["22.23.3","22.1.0","20.19.5"]}
            """);

        Assert.Equal("8.5", config.DefaultPhp);
        Assert.Equal("22", config.DefaultNode);
        Assert.True(config.Dumps);
        Assert.Equal(2525, config.Ports.Smtp);
        Assert.Equal(53531, config.Ports.Api);
        Assert.Equal(["8.5.11", "8.4.26"], config.Php.Select(p => p.FullVersion));
        Assert.True(config.Php[0].XdebugAvailable);
        Assert.False(config.Php[1].XdebugAvailable);
        Assert.Equal(["22", "20"], config.NodeMajors);
        Assert.Equal(@"C:\Users\x\AppData\Roaming\Flock", config.Root);
    }

    [Fact]
    public void DecodesConfigWithNullsAndMissingPorts()
    {
        var config = FlockJson.Decode<FlockConfig>("""
            {"tld":"test","default_php":"8.5","default_node":"22","root":"/r","ports":{"http":80},"parked":null,"php":null,"node":null}
            """);
        Assert.Empty(config.Parked);
        Assert.Empty(config.Php);
        Assert.Empty(config.Node);
        Assert.Equal(0, config.Ports.Https);
        Assert.True(config.PhpAutoBuild);
    }

    [Fact]
    public void DecodesServices()
    {
        var services = FlockJson.Decode<List<Service>>("""
            [{"id":"postgres","name":"PostgreSQL","category":"Database","version":"18.6.0","port":5432,"installed":true,
              "enabled":true,"running":true,"stoppable":true,
              "env":"DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1","log":"postgres","url":"postgres://postgres@127.0.0.1:5432"},
             {"id":"web","name":"Web Server","category":"Core","version":"","port":443,"installed":true,
              "enabled":true,"running":true,"stoppable":false,"env":"","log":"web","url":""},
             {"id":"dns","category":"Core","installed":false,"enabled":true,"running":false}]
            """);

        Assert.Equal(3, services.Count);
        Assert.True(services[0].IsDatabase);
        Assert.Contains("DB_HOST=127.0.0.1", services[0].Env);
        Assert.False(services[1].Stoppable);
        Assert.False(services[1].IsDatabase);
        Assert.False(services[1].HasConfigurablePort);
        Assert.Equal("dns", services[2].Name);
    }

    [Fact]
    public void SiteDecodesOptionalNode()
    {
        var site = FlockJson.Decode<Status.Site>("""{"name":"shop","domain":"shop.test","path":"/c/shop","php":"8.5","node":"22","secured":true,"parked":false}""");
        Assert.Equal("22", site.Node);
    }

    [Fact]
    public void EncodesOnlyChangedSettings()
    {
        Assert.Equal("""{"default_php":"8.4"}""", FlockJson.Encode(new SettingsChange(DefaultPhp: "8.4")));
        Assert.Equal("""{"php_auto_build":false}""", FlockJson.Encode(new SettingsChange(PhpAutoBuild: false)));
        Assert.Equal("""{"php":""}""", FlockJson.Encode(new SiteChange(Php: "")));
    }
}
