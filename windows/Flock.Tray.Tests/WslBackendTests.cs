using System.Diagnostics;
using System.Formats.Asn1;
using System.Net;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

/// <summary>
/// A daemon in WSL is set up and updated in its distro. Here the app sets up only what Windows
/// needs to open its sites, keeps the distro running and answers its names.
/// </summary>
public sealed class WslBackendTests : IDisposable
{
    private readonly DirectoryInfo directory = Directory.CreateTempSubdirectory("flock-wsl-");
    private readonly RecordingRunner runner = new();
    private readonly FlockBackend wsl = FlockBackend.ForRoot(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock");

    public void Dispose() => directory.Delete(recursive: true);

    private sealed class RecordingRunner : CliProcessRunner
    {
        public List<string> Runs { get; } = [];

        public override Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments, TimeSpan? deadline = null, CancellationToken cancellationToken = default)
        {
            Runs.Add(string.Join(' ', [fileName, .. arguments]));
            return Task.FromResult(new CliProcessResult(true, 0, ""));
        }
    }

    private readonly List<string> elevated = [];
    private bool trusted, rule, policy;
    private int? exitCode = 0;

    /// <summary>The distro's Flock folder (config.json and the CA), as the share shows it.</summary>
    private string Root(string? tld = "test", bool ca = true)
    {
        var root = Path.Combine(directory.FullName, "flock");
        Directory.CreateDirectory(Path.Combine(root, "certs"));
        if (tld is not null)
        {
            File.WriteAllText(Path.Combine(root, "config.json"), $$"""{"tld": "{{tld}}"}""");
        }
        if (ca)
        {
            File.WriteAllText(Path.Combine(root, "certs", "ca.crt"), "the distro's CA");
        }
        return root;
    }

    private WslWindowsSetup Setup(string root, string? cli = @"C:\Flock\flock.exe", bool realCa = false) =>
        new(wsl, root, () => cli, _ => trusted, _ => rule, (file, arguments, _) =>
        {
            elevated.Add(string.Join(' ', [file, .. arguments]));
            // The elevated program reads the copy of the CA it is given.
            var copy = arguments[arguments.ToList().IndexOf("--ca") + 1];
            Assert.Equal(File.ReadAllText(Path.Combine(root, "certs", "ca.crt")), File.ReadAllText(copy));
            if (exitCode is > 0)
            {
                File.WriteAllText(arguments[^1], "trust CA: access denied\n");
            }
            return Task.FromResult(exitCode);
        }, realCa ? null : (_, _) => null, () => policy);

    /// <summary>Writes a CA as Flock makes it: limited to the names given, or to none of them.</summary>
    private static void WriteCa(string root, params string[] permitted) => WriteCa(root, permitted, []);

    private static void WriteCa(string root, string[] permitted, string[] excluded, bool critical = true)
    {
        using var key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        var request = new CertificateRequest("CN=Flock Local CA (test)", key, HashAlgorithmName.SHA256);
        request.CertificateExtensions.Add(new X509BasicConstraintsExtension(true, false, 0, true));
        if (permitted.Length > 0)
        {
            var writer = new AsnWriter(AsnEncodingRules.DER);
            using (writer.PushSequence())
            using (writer.PushSequence(new Asn1Tag(TagClass.ContextSpecific, 0)))
            {
                foreach (var name in permitted)
                {
                    using (writer.PushSequence())
                    {
                        writer.WriteCharacterString(UniversalTagNumber.IA5String, name, new Asn1Tag(TagClass.ContextSpecific, 2));
                    }
                }
            }
            if (excluded.Length > 0)
            {
                // Appended to the outer sequence by hand: the writer above has closed it.
                var inner = new AsnWriter(AsnEncodingRules.DER);
                using (inner.PushSequence(new Asn1Tag(TagClass.ContextSpecific, 1)))
                {
                    foreach (var name in excluded)
                    {
                        using (inner.PushSequence())
                        {
                            inner.WriteCharacterString(UniversalTagNumber.IA5String, name, new Asn1Tag(TagClass.ContextSpecific, 2));
                        }
                    }
                }
                var both = new AsnWriter(AsnEncodingRules.DER);
                using (both.PushSequence())
                {
                    both.WriteEncodedValue(new AsnReader(writer.Encode(), AsnEncodingRules.DER).ReadSequence().ReadEncodedValue().Span);
                    both.WriteEncodedValue(inner.Encode());
                }
                writer = both;
            }
            request.CertificateExtensions.Add(new X509Extension("2.5.29.30", writer.Encode(), critical));
        }
        using var certificate = request.CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-1), DateTimeOffset.UtcNow.AddDays(30));
        File.WriteAllText(Path.Combine(root, "certs", "ca.crt"), certificate.ExportCertificatePem());
    }

    [Fact]
    public async Task ACaForTheTldIsHandedToTheElevatedStep()
    {
        var root = Root();
        WriteCa(root, ".test", "test", "localhost");
        var installation = Installation(Setup(root, realCa: true));

        Assert.False(await installation.IsSetUpAsync());
        Assert.True((await installation.InstallAsync()).Ok);
        Assert.Single(elevated);
    }

    [Fact]
    public async Task ACaMadeForAnotherTldIsNotTrustedAndSetupSaysSo()
    {
        // A CA from before the distro's TLD changed: Windows may trust it already, but it can't sign *.test sites.
        trusted = rule = true;
        var root = Root();
        WriteCa(root, ".wsl", "wsl", "localhost");
        var installation = Installation(Setup(root, realCa: true));

        Assert.False(await installation.IsSetUpAsync());
        var result = await installation.InstallAsync();
        Assert.False(result.Ok);
        Assert.Contains("was made for *.wsl, not for *.test", result.Output);
        Assert.Empty(elevated);
    }

    [Theory]
    [InlineData]
    [InlineData(".test", "test", "localhost", "com")]
    public async Task ACaThatCouldSignOtherNamesIsNotTrusted(params string[] permitted)
    {
        var root = Root();
        WriteCa(root, permitted);
        var installation = Installation(Setup(root, realCa: true));

        Assert.False(await installation.IsSetUpAsync());
        Assert.False((await installation.InstallAsync()).Ok);
        Assert.Empty(elevated);
    }

    [Fact]
    public async Task ACaThatExcludesNamesIsNotTrusted()
    {
        // Permitting *.test and excluding it again leaves a CA that can't sign the sites.
        var root = Root();
        WriteCa(root, [".test", "test", "localhost"], [".test"]);
        var installation = Installation(Setup(root, realCa: true));

        Assert.False(await installation.IsSetUpAsync());
        Assert.False((await installation.InstallAsync()).Ok);
        Assert.Empty(elevated);
    }

    [Fact]
    public async Task TheCaThatIsCheckedIsTheCopyTheElevatedStepGets()
    {
        // The distro's file can change between a check and a copy: the copy is what is checked.
        var root = Root();
        var checkedPaths = new List<string>();
        string? handed = null;
        var setup = new WslWindowsSetup(wsl, root, () => @"C:\Flock\flock.exe", _ => false, _ => false, (_, arguments, _) =>
        {
            handed = arguments[arguments.ToList().IndexOf("--ca") + 1];
            return Task.FromResult<int?>(0);
        }, (path, _) =>
        {
            checkedPaths.Add(path);
            return null;
        }, () => false);

        Assert.True((await setup.SetUpAsync()).Ok);
        Assert.Equal(handed, Assert.Single(checkedPaths));
        Assert.NotEqual(Path.Combine(root, "certs", "ca.crt"), handed);
    }

    [Fact]
    public async Task TheCheckedCopyCannotBeReplacedWhileSetupIsElevated()
    {
        var root = Root();
        Exception? writeError = null;
        var setup = new WslWindowsSetup(wsl, root, () => @"C:\Flock\flock.exe", _ => false, _ => false, (_, arguments, _) =>
        {
            var handed = arguments[arguments.ToList().IndexOf("--ca") + 1];
            try
            {
                using var replaced = new FileStream(handed, FileMode.Create, FileAccess.Write, FileShare.ReadWrite);
            }
            catch (Exception error)
            {
                writeError = error;
            }
            return Task.FromResult<int?>(0);
        }, (_, _) => null, () => false);

        Assert.True((await setup.SetUpAsync()).Ok);
        Assert.True(writeError is IOException or UnauthorizedAccessException, writeError?.ToString());
    }

    [Fact]
    public async Task ACaWhoseNameLimitsAreNotCriticalIsNotTrusted()
    {
        var root = Root();
        WriteCa(root, [".test", "test", "localhost"], [], critical: false);
        var installation = Installation(Setup(root, realCa: true));

        Assert.False(await installation.IsSetUpAsync());
        var result = await installation.InstallAsync();
        Assert.False(result.Ok);
        Assert.Contains("aren't marked critical", result.Output);
        Assert.Empty(elevated);
    }

    [Fact]
    public async Task ACaThatIsNotACertificateNeedsSetup()
    {
        var installation = Installation(Setup(Root(), realCa: true));

        Assert.False(await installation.IsSetUpAsync());
    }

    [Fact]
    public async Task RulesFromGroupPolicyLeaveTheRuleOutOfTheCheckAndSetupSaysSo()
    {
        // Windows ignores local rules then, so setup can't change where *.test goes.
        policy = trusted = true;
        var installation = Installation(Setup(Root()));
        Assert.True(await installation.IsSetUpAsync());

        trusted = false;
        installation = Installation(Setup(Root()));
        Assert.False(await installation.IsSetUpAsync());
        var result = await installation.InstallAsync();
        Assert.True(result.Ok);
        Assert.Contains("Group Policy", result.Output);
        Assert.DoesNotContain("is sent to", result.Output);
    }

    [Fact]
    public async Task NotNowLeavesSetupForThisRun()
    {
        var installation = Installation();
        Assert.False(await installation.IsSetUpAsync());

        Assert.True(installation.SkipSetup());

        Assert.True(await installation.IsSetUpAsync());
        Assert.Empty(elevated);
        // A daemon on Windows can't be shown before its setup.
        Assert.False(new InstallationService(runner, new PreferencesService(Path.Combine(directory.FullName, "tray.json")), backend: FlockBackend.Local).SkipSetup());
    }

    private InstallationService Installation(WslWindowsSetup? setup = null) =>
        new(runner, new PreferencesService(Path.Combine(directory.FullName, "tray.json")), backend: wsl, wslSetup: setup ?? Setup(Root()));

    [Fact]
    public async Task ItIsSetUpWhenWindowsResolvesAndTrustsTheDistrosSites()
    {
        trusted = rule = true;

        Assert.True(await Installation().IsSetUpAsync());
        Assert.Empty(runner.Runs);
    }

    [Theory]
    [InlineData(false, true)]
    [InlineData(true, false)]
    public async Task ItNeedsSetupWithoutTheTrustOrTheRule(bool hasTrust, bool hasRule)
    {
        (trusted, rule) = (hasTrust, hasRule);
        var installation = Installation();

        Assert.True(installation.CanSetUp);
        Assert.False(await installation.IsSetUpAsync());
    }

    [Fact]
    public async Task NothingIsAskedBeforeTheDistrosFlockIsInstalled()
    {
        var installation = Installation(Setup(Root(ca: false)));

        Assert.True(await installation.IsSetUpAsync());
        var result = await installation.InstallAsync();
        Assert.False(result.Ok);
        Assert.Contains("`flk install` in WSL (Ubuntu)", result.Output);
        Assert.Empty(elevated);
    }

    [Fact]
    public async Task ItNeverUpdatesTheCliOrChecksPortsHere()
    {
        var installation = Installation();

        Assert.False(await installation.NeedsUpdateAsync());
        Assert.Empty(await installation.ConflictsAsync());
        Assert.Empty(runner.Runs);
    }

    [Fact]
    public async Task SetupRunsFlocksElevatedStepForTheDistrosCaAndTld()
    {
        var lines = new List<string>();
        var result = await Installation(Setup(Root(tld: "dev"))).InstallAsync(lines.Add);

        Assert.True(result.Ok);
        var run = Assert.Single(elevated);
        Assert.StartsWith(@"C:\Flock\flock.exe privileged-setup --tld dev --ca ", run);
        Assert.Contains(@"\Flock\hosts.txt --no-helper --result-file ", run);
        // Only Flock's own setup on Windows removes: this must leave its helper and rule alone.
        Assert.DoesNotContain("--remove", run);
        Assert.Equal(["✓ every *.dev name (any subdomain) is sent to Flock in WSL (Ubuntu)", "✓ the certificate authority of Flock in WSL (Ubuntu) is trusted"], lines);
        Assert.Empty(runner.Runs);
    }

    [Fact]
    public async Task ADeclinedPromptIsReportedAndNotAskedAgainInThisRun()
    {
        exitCode = null;
        var installation = Installation();

        var result = await installation.InstallAsync();

        Assert.False(result.Ok);
        Assert.Contains(WslWindowsSetup.Declined, result.Output);
        // The daemon serves the sites either way: the app must not stay at "Not set up".
        Assert.True(await installation.IsSetUpAsync());
    }

    [Fact]
    public async Task AFailedSetupSaysWhatTheElevatedStepReported()
    {
        exitCode = 1;
        var installation = Installation();

        var result = await installation.InstallAsync();

        Assert.False(result.Ok);
        Assert.Equal("Error: trust CA: access denied", result.Output);
        // Unlike a declined prompt, a failed step is still not set up, so the tray can offer it again.
        Assert.False(await installation.IsSetUpAsync());
    }

    [Theory]
    [InlineData("test'; Remove-Item C:\\ -Recurse; '")]
    [InlineData("a.b")]
    [InlineData("-x")]
    [InlineData("TEST")]
    public async Task ATldFromTheDistroThatIsNotANameIsNeverHandedToTheElevatedStep(string tld)
    {
        var root = Root(tld: null);
        File.WriteAllText(Path.Combine(root, "config.json"), System.Text.Json.JsonSerializer.Serialize(new { tld }));
        var installation = Installation(Setup(root));

        Assert.True(await installation.IsSetUpAsync());
        Assert.False((await installation.InstallAsync()).Ok);
        Assert.Empty(elevated);
    }

    [Fact]
    public void TheTldDefaultsToTestWithoutAConfig()
    {
        Assert.Equal("test", Setup(Root(tld: null)).Tld());
        Assert.Equal("dev", Setup(Root(tld: "dev")).Tld());
    }

    [Fact]
    public async Task WithoutAWindowsCliThereIsNoSetupToRun()
    {
        var installation = Installation(Setup(Root(), cli: null));

        Assert.False(installation.CanSetUp);
        Assert.False((await installation.InstallAsync()).Ok);
        Assert.Empty(elevated);
    }

    [Fact]
    public async Task TheAppHoldsTheDistroOpenUntilItStops()
    {
        var held = new TaskCompletionSource<ProcessStartInfo>(TaskCreationOptions.RunContinuationsAsynchronously);
        var service = new WslKeepAliveService(wsl, hold: async (start, token) =>
        {
            held.TrySetResult(start);
            await Task.Delay(Timeout.Infinite, token);
        });

        await service.StartAsync(CancellationToken.None);
        var start = await held.Task.WaitAsync(TimeSpan.FromSeconds(5));
        await service.StopAsync(CancellationToken.None);

        Assert.Equal("wsl.exe", start.FileName);
        Assert.Equal(["-d", "Ubuntu", "--exec", "sleep", "infinity"], start.ArgumentList);
        Assert.True(start.CreateNoWindow);
        Assert.True(service.ExecuteTask!.IsCompletedSuccessfully);
    }

    [Fact]
    public async Task ADistroThatStoppedIsStartedAgain()
    {
        var clock = new Microsoft.Extensions.Time.Testing.FakeTimeProvider();
        var holds = 0;
        var second = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var service = new WslKeepAliveService(wsl, hold: (_, _) =>
        {
            if (Interlocked.Increment(ref holds) == 2)
            {
                second.TrySetResult();
            }
            return Task.CompletedTask;
        }, timeProvider: clock);

        await service.StartAsync(CancellationToken.None);
        // The service runs on the thread pool: advance the clock until its retry delay has passed.
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (!second.Task.IsCompleted && DateTime.UtcNow < deadline)
        {
            Assert.True(Volatile.Read(ref holds) <= 1);
            clock.Advance(WslKeepAliveService.RetryDelay);
            await Task.Delay(10);
        }
        await service.StopAsync(CancellationToken.None);

        Assert.True(second.Task.IsCompleted);
    }

    [Fact]
    public async Task AProcessInTheJobEndsWhenTheJobCloses()
    {
        // What Windows does with the app's job when the app is killed: its handle closes.
        var start = new ProcessStartInfo("ping.exe", "-n 60 127.0.0.1") { UseShellExecute = false, CreateNoWindow = true };
        using var process = Process.Start(start)!;
        var job = KillOnCloseJob.Create();
        try
        {
            Assert.NotNull(job);
            Assert.True(job.Add(process));
            Assert.False(process.HasExited);
        }
        finally
        {
            job?.Dispose();
        }

        await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(5));
    }

    [Fact]
    public async Task ALocalDaemonNeedsNothingKeptOpen()
    {
        var holds = 0;
        var service = new WslKeepAliveService(FlockBackend.Local, hold: (_, _) =>
        {
            holds++;
            return Task.CompletedTask;
        });

        await service.StartAsync(CancellationToken.None);
        await service.ExecuteTask!;

        Assert.Equal(0, holds);
    }

    [Fact]
    public async Task TheAppAnswersTheDaemonsTldOnWindows()
    {
        var service = new WslDnsService(wsl, _ => Task.FromResult("test"), endpoint: new IPEndPoint(IPAddress.Loopback, 0));
        await service.StartAsync(CancellationToken.None);
        var server = await service.Listening.WaitAsync(TimeSpan.FromSeconds(5));
        byte[] query = [0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, (byte)'a', (byte)'p', (byte)'p', 4, (byte)'t', (byte)'e', (byte)'s', (byte)'t', 0, 0, 1, 0, 1];

        using var client = new UdpClient(AddressFamily.InterNetwork);
        await client.SendAsync(query, server);
        var reply = (await client.ReceiveAsync().WaitAsync(TimeSpan.FromSeconds(5))).Buffer;
        await service.StopAsync(CancellationToken.None);

        Assert.Equal([0x12, 0x34], reply[..2]);
        Assert.Equal([127, 0, 0, 1], reply[^4..]);
    }

    [Fact]
    public async Task ALocalDaemonAnswersItsOwnLookups()
    {
        var asked = false;
        var service = new WslDnsService(FlockBackend.Local, _ =>
        {
            asked = true;
            return Task.FromResult("test");
        }, endpoint: new IPEndPoint(IPAddress.Loopback, 0));

        await service.StartAsync(CancellationToken.None);
        await service.ExecuteTask!;

        Assert.False(asked);
        Assert.False(service.Listening.IsCompleted);
    }

    [Fact]
    public void TheDefaultBackendFollowsFlockHome()
    {
        var installation = new InstallationService(runner, new PreferencesService(Path.Combine(directory.FullName, "tray.json")));

        Assert.Equal(FlockBackend.Current, installation.Backend);
    }
}
