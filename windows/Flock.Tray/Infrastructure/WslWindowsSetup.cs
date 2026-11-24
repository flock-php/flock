using System.ComponentModel;
using System.Diagnostics;
using System.Formats.Asn1;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using System.Text.RegularExpressions;
using Flock.Kit;
using Microsoft.Win32;

namespace Flock.Tray.Infrastructure;

/// <summary>
/// What Windows needs to open the sites of a daemon in WSL: the rule that sends the daemon's TLD
/// to 127.0.0.1 (where <see cref="WslDnsService"/> answers), and the distro's certificate
/// authority among the trusted roots. Both belong to the machine, so setting them up asks for
/// administrator permission once; it runs the same step as Flock's own setup on Windows
/// (`flock privileged-setup`), without the hosts helper. Nothing here removes them again.
/// </summary>
public sealed partial class WslWindowsSetup(FlockBackend backend, string root, Func<string?> cli,
    Func<string, bool>? trusted = null, Func<string, bool>? ruleInstalled = null,
    Func<string, IReadOnlyList<string>, CancellationToken, Task<int?>>? elevate = null, Func<string, string, string?>? caProblem = null,
    Func<bool>? policyManaged = null)
{
    /// <summary>What a declined permission prompt reports, as `flock install` words it.</summary>
    public const string Declined = "administrator permission was declined";

    private const string RuleKey = @"SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig";
    private const string PolicyRuleKey = @"SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig";

    private readonly Func<string, bool> trusted = trusted ?? IsTrusted;
    private readonly Func<string, bool> ruleInstalled = ruleInstalled ?? IsRuleInstalled;
    private readonly Func<string, IReadOnlyList<string>, CancellationToken, Task<int?>> elevate = elevate ?? ElevateAsync;
    private readonly Func<string, string, string?> caProblem = caProblem ?? CaProblem;
    private readonly Func<bool> policyManaged = policyManaged ?? IsPolicyManaged;

    /// <summary>
    /// The Windows CLI that does the setup: the one shipped with the app, or the one an earlier
    /// Flock for Windows left in %AppData%. Never the one FLOCK_HOME names, which is in the distro.
    /// </summary>
    public static string? WindowsCli()
    {
        if (Installation.BundledFlock is { } bundled)
        {
            return bundled;
        }
        var installed = Path.Combine(WindowsRoot, "bin", "flock.exe");
        return File.Exists(installed) ? installed : null;
    }

    /// <summary>Flock's folder on Windows, whatever FLOCK_HOME says.</summary>
    private static string WindowsRoot => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "Flock");

    private string CaPath => Path.Combine(root, "certs", "ca.crt");

    /// <summary>Whether this app can do the setup: it needs a Windows flock.exe to run.</summary>
    public bool IsAvailable => cli() is not null;

    /// <summary>
    /// The daemon's TLD, from the distro's config.json ("test" when it names none), or null when
    /// it isn't one to hand to an elevated command: the file is the distro's, not this app's.
    /// </summary>
    public string? Tld()
    {
        var name = "test";
        try
        {
            using var config = JsonDocument.Parse(File.ReadAllText(Path.Combine(root, "config.json")));
            if (config.RootElement.ValueKind == JsonValueKind.Object && config.RootElement.TryGetProperty("tld", out var value)
                && value.ValueKind == JsonValueKind.String && value.GetString() is { Length: > 0 } text)
            {
                name = text;
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException)
        {
            // No config yet (or the distro is off): the default.
        }
        return ValidTld().IsMatch(name) ? name : null;
    }

    [GeneratedRegex("^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")]
    private static partial Regex ValidTld();

    /// <summary>
    /// Whether Windows resolves and trusts the distro's sites. Also true when that can't be told
    /// or changed from here: the distro's Flock isn't installed yet, or its files can't be read.
    /// A file that isn't a certificate, or a CA that can't sign the TLD's sites, is not set up,
    /// whatever Windows trusts: setup says why.
    /// Rules from Group Policy make Windows ignore the rule setup adds, so the rule isn't asked
    /// for then: setup couldn't change what Windows does.
    /// </summary>
    public bool IsSetUp()
    {
        try
        {
            return !File.Exists(CaPath) || Tld() is not { } tld
                || (caProblem(CaPath, tld) is null && trusted(CaPath) && (policyManaged() || ruleInstalled(tld)));
        }
        catch (CryptographicException)
        {
            // The file is there but isn't a certificate. Setup can say so; hiding it looks set up.
            return false;
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Security.SecurityException)
        {
            return true;
        }
    }

    /// <summary>Sets both up, with one permission prompt. Reports like `flock install` does.</summary>
    public async Task<Installation.Result> SetUpAsync(CancellationToken cancellationToken = default)
    {
        var name = $"Flock in WSL ({backend.WslDistro})";
        if (cli() is not { } exe)
        {
            return new Installation.Result(false, $"Error: this build of Flock has no flock.exe to set Windows up with — see \"The daemon in WSL2\" in windows/README.md.");
        }
        if (!File.Exists(CaPath))
        {
            return new Installation.Result(false, $"Error: {name} has no certificate authority yet — {backend.InstallHint} first.");
        }
        if (Tld() is not { } tld)
        {
            return new Installation.Result(false, $"Error: the TLD in the config.json of {name} is not one Windows can be set up for.");
        }
        // The elevated program gets copies in a Windows folder: a distro's share isn't always
        // there for an administrator, and it reports its error through a file. The copy is also
        // the one that is checked, so the CA that is trusted is the CA that passed. It stays open
        // until that program finishes: the distro can write this temp folder through /mnt/c, and
        // the permission prompt is long enough to swap the file after the check.
        var work = Directory.CreateTempSubdirectory("flock-wsl-");
        try
        {
            var ca = Path.Combine(work.FullName, "ca.crt");
            var result = Path.Combine(work.FullName, "result.txt");
            File.Copy(CaPath, ca);
            // No delete share: while this handle is open, the distro can't replace or delete the copy.
            await using var caLock = new FileStream(ca, FileMode.Open, FileAccess.Read, FileShare.Read);
            try
            {
                if (caProblem(ca, tld) is { } problem)
                {
                    return new Installation.Result(false, $"Error: the certificate authority of {name} {problem}. Windows isn't asked to trust it. " +
                        $"Flock makes a new one for *.{tld} when the certs folder in the distro's Flock folder is removed and `{FlockBackend.LinuxCli} install` runs there.");
                }
            }
            catch (CryptographicException error)
            {
                return new Installation.Result(false, $"Error: the certificate authority of {name} can't be read: {error.Message}");
            }
            // --source is the hosts helper's file, checked even though --no-helper leaves the helper out.
            var code = await elevate(exe,
                new[] { "privileged-setup", "--tld", tld, "--ca", ca, "--source", Path.Combine(WindowsRoot, "hosts.txt"), "--no-helper", "--result-file", result },
                cancellationToken);
            _ = caLock;
            if (code is null)
            {
                return new Installation.Result(false, $"Error: {Declined}");
            }
            if (code != 0)
            {
                var problem = File.Exists(result) ? File.ReadAllText(result).Trim() : "";
                return new Installation.Result(false, "Error: " + (problem.Length > 0 ? problem : $"the elevated setup failed with exit code {code}"));
            }
            var rule = policyManaged()
                ? $"! your organization manages DNS rules (Group Policy), so Windows ignores Flock's rule for .{tld}"
                : $"✓ every *.{tld} name (any subdomain) is sent to {name}";
            return new Installation.Result(true, $"{rule}\n✓ the certificate authority of {name} is trusted");
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or Win32Exception or InvalidOperationException)
        {
            return new Installation.Result(false, "Error: " + error.Message);
        }
        finally
        {
            try
            {
                work.Delete(recursive: true);
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException)
            {
                // Windows clears the temp folder.
            }
        }
    }

    /// <summary>
    /// Why the CA in the file shouldn't be trusted for tld, or null. Flock limits its CA to the TLD
    /// it was made for (and localhost) with name constraints, so a CA from before the TLD changed
    /// can't sign the sites' certificates, and one without constraints could sign any name: it
    /// would get this machine's trust for every site, from a key that lives in the distro.
    /// </summary>
    private static string? CaProblem(string path, string tld)
    {
        using var certificate = X509CertificateLoader.LoadCertificateFromFile(path);
        if (certificate.Extensions["2.5.29.30"] is { Critical: false })
        {
            return "has name limits that aren't marked critical, so a program that doesn't implement them would trust it for every name";
        }
        if (PermittedDnsNames(certificate) is not { Count: > 0 } permitted)
        {
            return $"isn't limited to *.{tld} names";
        }
        string[] own = [tld, "." + tld, "localhost"];
        if (!permitted.Any(name => name == tld || name == "." + tld) || permitted.Except(own, StringComparer.OrdinalIgnoreCase).Any())
        {
            return $"was made for {string.Join(", ", permitted.Except(new[] { "localhost" }).Select(name => "*." + name.TrimStart('.')).Distinct())}, not for *.{tld}";
        }
        return null;
    }

    /// <summary>
    /// The DNS names a CA's name constraints permit, or null when it has no such list, the list
    /// isn't marked critical, it permits other kinds of names, or it excludes names (which this
    /// app doesn't judge). A program that doesn't implement name constraints ignores them unless
    /// they are critical, so a non-critical list is the same as no limit.
    /// </summary>
    internal static IReadOnlyList<string>? PermittedDnsNames(X509Certificate2 certificate)
    {
        if (certificate.Extensions["2.5.29.30"] is not { Critical: true } constraints)
        {
            return null;
        }
        try
        {
            // NameConstraints ::= SEQUENCE { permittedSubtrees [0] SEQUENCE OF GeneralSubtree, … };
            // GeneralSubtree ::= SEQUENCE { base GeneralName, … }; a dNSName is [2] IA5String.
            var permittedTag = new Asn1Tag(TagClass.ContextSpecific, 0, isConstructed: true);
            var dnsTag = new Asn1Tag(TagClass.ContextSpecific, 2);
            var sequence = new AsnReader(constraints.RawData, AsnEncodingRules.DER).ReadSequence();
            if (!sequence.HasData || !sequence.PeekTag().HasSameClassAndValue(permittedTag))
            {
                return null;
            }
            var names = new List<string>();
            var subtrees = sequence.ReadSequence(permittedTag);
            while (subtrees.HasData)
            {
                var subtree = subtrees.ReadSequence();
                if (!subtree.PeekTag().HasSameClassAndValue(dnsTag))
                {
                    return null;
                }
                names.Add(subtree.ReadCharacterString(UniversalTagNumber.IA5String, dnsTag));
            }
            // Anything after the permitted names is excludedSubtrees, which could take them back.
            return sequence.HasData ? null : names;
        }
        catch (AsnContentException)
        {
            return null;
        }
    }

    /// <summary>Whether the certificate in the file is among the machine's trusted roots.</summary>
    private static bool IsTrusted(string path)
    {
        using var certificate = X509CertificateLoader.LoadCertificateFromFile(path);
        using var store = new X509Store(StoreName.Root, StoreLocation.LocalMachine);
        store.Open(OpenFlags.ReadOnly | OpenFlags.OpenExistingOnly);
        return store.Certificates.Find(X509FindType.FindByThumbprint, certificate.Thumbprint, validOnly: false).Count > 0;
    }

    /// <summary>
    /// Whether a local NRPT rule sends .&lt;tld&gt; to 127.0.0.1, whoever made it: Flock's setup, or
    /// a rule added by hand (the earlier instructions).
    /// </summary>
    private static bool IsRuleInstalled(string tld)
    {
        using var rules = Registry.LocalMachine.OpenSubKey(RuleKey);
        foreach (var id in rules?.GetSubKeyNames() ?? [])
        {
            using var rule = rules!.OpenSubKey(id);
            if (rule?.GetValue("Name") is string[] names && names.Contains("." + tld, StringComparer.OrdinalIgnoreCase)
                && rule.GetValue("GenericDNSServers") is string servers
                && servers.Split(';', StringSplitOptions.TrimEntries).Contains("127.0.0.1"))
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// Whether Group Policy deploys NRPT rules, which make Windows ignore the local ones (as
    /// PolicyManaged in internal/dns/nrpt_windows.go).
    /// </summary>
    private static bool IsPolicyManaged()
    {
        using var rules = Registry.LocalMachine.OpenSubKey(PolicyRuleKey);
        return rules?.SubKeyCount > 0;
    }

    /// <summary>
    /// Runs the program as administrator (Windows asks) and waits for it. Null when the prompt
    /// was declined.
    /// </summary>
    private static async Task<int?> ElevateAsync(string fileName, IReadOnlyList<string> arguments, CancellationToken cancellationToken)
    {
        var start = new ProcessStartInfo(fileName) { UseShellExecute = true, Verb = "runas", WindowStyle = ProcessWindowStyle.Hidden };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }
        const int cancelled = 1223;
        try
        {
            using var process = Process.Start(start) ?? throw new InvalidOperationException("The elevated setup did not start.");
            await process.WaitForExitAsync(cancellationToken);
            return process.ExitCode;
        }
        catch (Win32Exception error) when (error.NativeErrorCode == cancelled)
        {
            return null;
        }
    }
}
