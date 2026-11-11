using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     Asks for the domain to serve a chosen folder as; left empty, it is served as &lt;folder&gt;.test, as
///     <c>flock link</c> does.
/// </summary>
internal sealed partial class AddSiteView : UserControl
{
    private readonly string name;
    private readonly string tld;

    public AddSiteView(string folder, string tld)
    {
        name = SiteName(folder);
        this.tld = tld;
        InitializeComponent();
        DomainBox.PlaceholderText = $"{name}.{tld}";
        Update();
    }

    /// <summary>The domain typed, or null for the folder's.</summary>
    public string? Domain => DomainBox.Text.Trim() is { Length: > 0 } domain ? domain : null;

    /// <summary>A folder's name as a hostname label, the site name Flock gives it (config.SiteName).</summary>
    private static string SiteName(string folder)
    {
        var label = new string(folder.ToLowerInvariant().Select(c => c is >= 'a' and <= 'z' or >= '0' and <= '9' or '-' ? c : '-').ToArray()).Trim('-');
        return label.Length > 0 ? label : "app";
    }

    private void DomainChanged(object sender, TextChangedEventArgs args) => Update();

    // The daemon checks the name; this only shows where it would be served, as config.SetDomain reads it.
    private void Update()
    {
        var typed = (Domain ?? "").ToLowerInvariant().TrimEnd('.');
        if (typed.EndsWith("." + tld, StringComparison.Ordinal))
        {
            typed = typed[..^(tld.Length + 1)];
        }
        ExplanationText.Text = typed.Length == 0
            ? $"Flock serves it as {name}.{tld}, or as a domain of your choosing, like {name}.my-project.{tld}."
            : $"Flock serves it as {typed}.{tld}.";
    }
}
