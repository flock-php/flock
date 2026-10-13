using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>GET /v1/config — the persisted configuration plus what is installed.</summary>
public sealed class FlockConfig
{
    public string Tld { get; init; } = "test";
    public string DefaultPhp { get; init; } = "";
    public string DefaultNode { get; init; } = "";
    public bool Xdebug { get; init; }
    public bool Dumps { get; init; }
    public bool PhpAutoBuild { get; init; } = true;
    public string Root { get; init; } = "";
    public PortSet Ports { get; init; } = new();
    public List<string> Parked { get; init; } = [];
    public List<PhpVersion> Php { get; init; } = [];
    public List<string> Node { get; init; } = [];

    /// <summary>Node majors that are installed, newest first ("22.23.3" → "22").</summary>
    [JsonIgnore]
    public List<string> NodeMajors => Node.Select(v => v.Split('.')[0]).Distinct().ToList();

    public sealed class PortSet
    {
        public int Http { get; init; }
        public int Https { get; init; }
        public int Dns { get; init; }
        public int Smtp { get; init; }
        public int Postgres { get; init; }
        public int Redis { get; init; }
        public int Dumps { get; init; }
        public int Api { get; init; }
    }

    public sealed class PhpVersion
    {
        private readonly string? fullVersion;

        public string Version { get; init; } = "";
        public string FullVersion { get => fullVersion ?? Version; init => fullVersion = value; }
        public string Ini { get; init; } = "";
        public bool XdebugAvailable { get; init; }
    }
}

/// <summary>GET /v1/services — one background service (web server, database, mail catcher, ...).</summary>
public sealed class Service
{
    private readonly string? name;

    public string Id { get; init; } = "";
    public string Name { get => name ?? Id; init => name = value; }
    public string Category { get; init; } = "Other";
    public string Version { get; init; } = "";
    public int Port { get; init; }
    public bool Installed { get; init; }
    public bool Enabled { get; init; }
    public bool Running { get; init; }
    public bool Stoppable { get; init; }
    /// <summary>.env snippet to connect a Laravel app.</summary>
    public string Env { get; init; } = "";
    /// <summary>Name for /v1/logs/services/{name}.</summary>
    public string Log { get; init; } = "";
    /// <summary>Connection URL (e.g. for TablePlus).</summary>
    public string Url { get; init; } = "";

    /// <summary>Is this a database that a GUI client like TablePlus can open?</summary>
    [JsonIgnore]
    public bool IsDatabase => Category == "Database" && Url.Length > 0;

    /// <summary>The web server and hosts ports are tied to certificates and the hosts helper; `flock install` changes them.</summary>
    [JsonIgnore]
    public bool HasConfigurablePort => Port > 0 && Id != "web" && Id != "dns";
}

/// <summary>Body of POST /v1/settings; null fields are left out and stay unchanged.</summary>
public sealed record SettingsChange(
    bool? Xdebug = null,
    bool? Dumps = null,
    string? DefaultPhp = null,
    string? DefaultNode = null,
    bool? PhpAutoBuild = null);

/// <summary>Body of PATCH /v1/sites/{name}; null fields are left out. An empty Php means "use the default".</summary>
public sealed record SiteChange(bool? Secured = null, string? Php = null, string? Node = null);
