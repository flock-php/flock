using System.Security.Cryptography;
using System.Text;

namespace Flock.Tray;

/// <summary>Keeps local executables separate from the installed tray and from other local builds.</summary>
internal sealed record LaunchIdentity(bool IsDevelopment, string Key)
{
    public static LaunchIdentity Production { get; } = new(false, "Flock.Tray");
    public string MutexName => @"Local\" + Key;
    public string WindowClass => Key + ".NotifyWindow";
    public string DisplayName => IsDevelopment ? "Flock Dev" : "Flock";
    public string DataDirectory => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "Flock", IsDevelopment ? Key : "");

    public static LaunchIdentity ForExecutable(string executable, bool installed, bool forceDevelopment)
    {
        if (installed && !forceDevelopment)
        {
            return Production;
        }
        var path = Path.GetFullPath(executable).ToUpperInvariant();
        var hash = Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(path)))[..16];
        return new LaunchIdentity(true, "Flock.Tray.Dev." + hash);
    }
}
