using System.Globalization;
using System.Text.RegularExpressions;
using System.Xml.Linq;

namespace Flock.Kit.Tests;

/// <summary>
/// The translations of Flock.Kit and of the tray app (Flock.Tray), read from their .resx files, so that a
/// missing or broken translation fails on any OS — the tray's own tests run on Windows only.
/// </summary>
public partial class ResourcesTests
{
    public static TheoryData<string> Projects => ["Flock.Kit", "Flock.Tray"];

    [Theory]
    [MemberData(nameof(Projects))]
    public void EveryStringIsTranslated(string project)
    {
        var english = Read(project, "");
        foreach (var language in new[] { "cs", "es" })
        {
            var translated = Read(project, "." + language);
            Assert.Empty(english.Keys.Where(key => !translated.ContainsKey(key) && !key.EndsWith("_few", StringComparison.Ordinal))
                .Select(key => $"{language} misses {key}"));
            Assert.Empty(translated.Keys.Where(key => !english.ContainsKey(key) && !(language == "cs" && IsFewOf(key, english)))
                .Select(key => $"{language} has unknown {key}"));
            Assert.Empty(translated.Where(pair => pair.Value.Trim().Length == 0).Select(pair => $"{language} {pair.Key} is empty"));
        }
    }

    [Theory]
    [MemberData(nameof(Projects))]
    public void TranslationsKeepThePlaceholders(string project)
    {
        var english = Read(project, "");
        foreach (var language in new[] { "", ".cs", ".es" })
        {
            foreach (var (key, value) in Read(project, language))
            {
                var source = english.GetValueOrDefault(key) ?? english[key[..^"_few".Length] + "_other"];
                Assert.True(Placeholders(source).SetEquals(Placeholders(value)), $"{project}{language} {key}: \"{value}\" has other placeholders than \"{source}\"");
                if (value.Contains('{'))
                {
                    // Throws for a broken format string.
                    _ = string.Format(CultureInfo.InvariantCulture, value, Enumerable.Repeat<object>(new AnyFormat(), 10).ToArray());
                }
            }
        }
    }

    [Theory]
    [MemberData(nameof(Projects))]
    public void PluralsHaveTheirForms(string project)
    {
        var english = Read(project, "");
        var czech = Read(project, ".cs");
        var spanish = Read(project, ".es");
        foreach (var other in english.Keys.Where(key => key.EndsWith("_other", StringComparison.Ordinal)))
        {
            var stem = other[..^"_other".Length];
            Assert.True(english.ContainsKey(stem + "_one"), $"{stem}_one is missing");
            Assert.True(czech.ContainsKey(stem + "_few"), $"cs {stem}_few is missing");
            Assert.False(english.ContainsKey(stem + "_few") || spanish.ContainsKey(stem + "_few"), $"{stem}_few is Czech only");
        }
        foreach (var file in Sources(project, "*.cs"))
        {
            foreach (Match match in PluralCall().Matches(File.ReadAllText(file)))
            {
                var stem = match.Groups[1].Value;
                Assert.True(english.ContainsKey(stem + "_one") && english.ContainsKey(stem + "_other"), $"{Path.GetFileName(file)}: {stem} has no plural forms");
            }
        }
    }

    [Theory]
    [MemberData(nameof(Projects))]
    public void KeysDifferInMoreThanCase(string project)
    {
        // A .resx can't hold two names that differ only in case.
        Assert.Empty(Read(project, "").Keys.GroupBy(key => key, StringComparer.OrdinalIgnoreCase).Where(group => group.Count() > 1).Select(group => group.Key));
    }

    [Theory]
    [MemberData(nameof(Projects))]
    public void TheDesignerMatchesTheResources(string project)
    {
        // Strings.Designer.cs is generated from Strings.resx (ResXFileCodeGenerator): one property per string.
        var designer = File.ReadAllText(Path.Combine(Root, project, "Strings.Designer.cs"));
        var properties = DesignerProperty().Matches(designer).Select(match => match.Groups[1].Value).Order(StringComparer.Ordinal);
        Assert.Equal(Read(project, "").Keys.Order(StringComparer.Ordinal), properties);
    }

    [Fact]
    public void XamlUsesExistingStringsOnly()
    {
        var english = Read("Flock.Tray", "");
        foreach (var file in Sources("Flock.Tray", "*.xaml"))
        {
            var xaml = File.ReadAllText(file);
            Assert.Empty(LocKey().Matches(xaml).Select(match => match.Groups[1].Value).Where(key => !english.ContainsKey(key))
                .Select(key => $"{Path.GetFileName(file)}: no string {key}"));
        }
    }

    [Fact]
    public void XamlHasNoUntranslatedText()
    {
        // Product names and technical values stay as they are.
        string[] kept = ["Flock", "PHP", "Node", "PHP-CGI", "FrankenPHP", "Xdebug", "HTML", "9003", "127.0.0.1", "debug, develop"];
        foreach (var file in Sources("Flock.Tray", "*.xaml"))
        {
            var texts = XDocument.Load(file).Descendants().SelectMany(element => element.Attributes())
                .Where(attribute => TextAttributes.Contains(attribute.Name.LocalName) && attribute.Value.Length > 0 && !attribute.Value.StartsWith('{'))
                .Select(attribute => attribute.Value);
            Assert.Empty(texts.Where(text => !kept.Contains(text)).Select(text => $"{Path.GetFileName(file)}: \"{text}\""));
        }
    }

    /// <summary>An argument that takes any format specifier ({0:t}, {0:N0}).</summary>
    private sealed class AnyFormat : IFormattable
    {
        public string ToString(string? format, IFormatProvider? formatProvider) => "x";
    }

    private static readonly HashSet<string> TextAttributes =
        ["Text", "Content", "Header", "Title", "Label", "Description", "PlaceholderText", "ToolTipService.ToolTip", "AutomationProperties.Name", "BusyText"];

    private static bool IsFewOf(string key, Dictionary<string, string> english) =>
        key.EndsWith("_few", StringComparison.Ordinal) && english.ContainsKey(key[..^"_few".Length] + "_other");

    private static HashSet<string> Placeholders(string value) =>
        Placeholder().Matches(value.Replace("{{", "").Replace("}}", "")).Select(match => match.Groups[1].Value).ToHashSet();

    private static Dictionary<string, string> Read(string project, string language) =>
        XDocument.Load(Path.Combine(Root, project, $"Strings{language}.resx")).Root!.Elements("data")
            .ToDictionary(data => (string)data.Attribute("name")!, data => (string?)data.Element("value") ?? "");

    private static IEnumerable<string> Sources(string project, string pattern) =>
        Directory.EnumerateFiles(Path.Combine(Root, project), pattern, SearchOption.AllDirectories)
            .Where(file => !file.Contains($"{Path.DirectorySeparatorChar}obj{Path.DirectorySeparatorChar}", StringComparison.Ordinal)
                           && !file.Contains($"{Path.DirectorySeparatorChar}bin{Path.DirectorySeparatorChar}", StringComparison.Ordinal));

    /// <summary>windows\, found from the test's output folder upwards.</summary>
    private static string Root { get; } = FindRoot();

    private static string FindRoot()
    {
        for (var directory = new DirectoryInfo(AppContext.BaseDirectory); directory is not null; directory = directory.Parent)
        {
            if (File.Exists(Path.Combine(directory.FullName, "Flock.Tray", "Strings.resx")))
            {
                return directory.FullName;
            }
        }
        throw new DirectoryNotFoundException("windows\\Flock.Tray\\Strings.resx not found above " + AppContext.BaseDirectory);
    }

    [GeneratedRegex(@"\{(\d+)(?:[,:][^}]*)?\}")]
    private static partial Regex Placeholder();

    [GeneratedRegex(@"Plural\(Strings\.ResourceManager,\s*""(\w+)""")]
    private static partial Regex PluralCall();

    [GeneratedRegex(@"internal static string (\w+) \{")]
    private static partial Regex DesignerProperty();

    [GeneratedRegex(@"\{l:Loc Key=(\w+)\}")]
    private static partial Regex LocKey();
}
