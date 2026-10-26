namespace Flock.Kit;

/// <summary>
/// <c>flock://update</c>, opened by `flock update`: the app checks for an update of itself and
/// installs it. Updates come only from Flock's signed releases, so the link can't install anything else.
/// </summary>
public static class UpdateLink
{
    public static bool IsUpdateLink(string? text) =>
        !string.IsNullOrEmpty(text) && text.Length <= 200
        && Uri.TryCreate(text, UriKind.Absolute, out var uri)
        && uri.Scheme.Equals(InstallLink.Scheme, StringComparison.OrdinalIgnoreCase)
        && uri.Host.Equals("update", StringComparison.OrdinalIgnoreCase)
        && uri.AbsolutePath.Trim('/').Length == 0;
}