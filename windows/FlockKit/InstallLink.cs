namespace Flock.Kit;

/// <summary>
/// A link from the website or the docs that asks to install a service:
/// <c>flock://install/&lt;service&gt;</c> or <c>flock://install/&lt;service&gt;/&lt;version line&gt;</c>,
/// e.g. <c>flock://install/mysql/8.4</c> or <c>flock://install/postgres/18</c>.
///
/// Any web page can open such a link, so it never installs anything by itself: the app shows what
/// it would install and waits for the user to confirm (see Resolve).
/// </summary>
/// <param name="Service">The service id ("mysql").</param>
/// <param name="Line">The version line ("8.4", a PostgreSQL major); null for the newest.</param>
public sealed record InstallLink(string Service, string? Line = null)
{
    public const string Scheme = "flock";

    /// <summary>The link in a command line or message, or null when it isn't a valid install link.</summary>
    public static InstallLink? Parse(string? text)
    {
        if (string.IsNullOrEmpty(text) || text.Length > 200
            || !Uri.TryCreate(text, UriKind.Absolute, out var uri)
            || !uri.Scheme.Equals(Scheme, StringComparison.OrdinalIgnoreCase)
            || !uri.Host.Equals("install", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }
        var parts = Uri.UnescapeDataString(uri.AbsolutePath).Split('/', StringSplitOptions.RemoveEmptyEntries);
        if (parts.Length is < 1 or > 2 || !IsSafe(parts[0], 32) || (parts.Length == 2 && !IsSafe(parts[1], 16)))
        {
            return null;
        }
        return new InstallLink(parts[0].ToLowerInvariant(), parts.Length == 2 ? parts[1] : null);
    }

    /// <summary>Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.</summary>
    private static bool IsSafe(string part, int maxLength) =>
        part.Length > 0 && part.Length <= maxLength && part.All(c => char.IsAsciiLetterOrDigit(c) || c is '.' or '-');

    /// <summary>
    /// What the link asks for, measured against what this Flock offers and has installed. Null while
    /// the services and versions are still loading.
    /// </summary>
    public InstallLinkResolution? Resolve(IReadOnlyList<Service> services, ServiceCatalog? catalog, PostgresVersions? postgres)
    {
        if (services.Count == 0)
        {
            return null;
        }
        var service = services.FirstOrDefault(s => s.Id == Service);
        if (service is null)
        {
            return InstallLinkResolution.Unknown(Service);
        }
        if (!service.Available)
        {
            return InstallLinkResolution.Unavailable(service.Name, service.Unavailable);
        }

        if (service.Id == "postgres")
        {
            if (postgres is null)
            {
                return null;
            }
            var major = Line is null ? postgres.Majors.FirstOrDefault() : postgres.Majors.FirstOrDefault(m => m.Major == Line);
            if (major is null)
            {
                return InstallLinkResolution.UnknownVersion(service.Name, Line ?? "");
            }
            return major.IsInstalled
                ? InstallLinkResolution.AlreadyInstalled($"PostgreSQL {major.Major}")
                : InstallLinkResolution.Install(new InstallLinkTarget(service.Id, service.Name, major.Major, major.Major, major.Latest));
        }

        if (catalog is null)
        {
            return null;
        }
        var entry = catalog[service.Id];
        if (entry is null)
        {
            return InstallLinkResolution.Unknown(service.Name);
        }
        var target = Line is null ? entry.Lines.FirstOrDefault() : entry.Lines.FirstOrDefault(l => l.Id == Line);
        if (target is null)
        {
            return InstallLinkResolution.UnknownVersion(entry.Name, Line ?? "");
        }
        if (target.Installed)
        {
            return InstallLinkResolution.AlreadyInstalled($"{entry.Name} {target.Label}");
        }
        return InstallLinkResolution.Install(new InstallLinkTarget(entry.Id, entry.Name, target.Id, target.Label, target.Latest));
    }
}

/// <summary>What an install link would install once the user confirms.</summary>
/// <param name="Line">The line to pass to the daemon ("8.4", "18").</param>
/// <param name="Label">How the line reads ("8.4 LTS").</param>
/// <param name="Version">The release that gets installed; empty when not known yet.</param>
public sealed record InstallLinkTarget(string Service, string Name, string Line, string Label, string Version);

/// <summary>What the app tells the user about an install link: a question to confirm, or why there is nothing to install.</summary>
/// <param name="Target">Set when there is something to install.</param>
public sealed record InstallLinkResolution(string Title, string Message, InstallLinkTarget? Target = null)
{
    public static InstallLinkResolution Install(InstallLinkTarget target) => new(
        $"Install {target.Name} {target.Label}?",
        $"A link asked Flock to install {target.Name} {(target.Version.Length > 0 ? target.Version : target.Label)}. It runs on 127.0.0.1 for your sites.",
        target);

    public static InstallLinkResolution AlreadyInstalled(string name) =>
        new($"{name} is already installed", "You'll find it in Settings → Services.");

    public static InstallLinkResolution Unavailable(string name, string reason) =>
        new($"{name} isn't available on this computer", reason.Length > 0 ? reason : "Flock can't run it on this platform.");

    public static InstallLinkResolution Unknown(string name) =>
        new($"Flock doesn't offer {name}", NewerFlock);

    public static InstallLinkResolution UnknownVersion(string name, string line) =>
        new($"Flock doesn't offer {name} {line}", NewerFlock);

    private const string NewerFlock = "The link may be meant for a newer Flock. Update Flock, then try again.";
}
