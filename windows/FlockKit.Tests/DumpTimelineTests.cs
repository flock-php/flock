namespace Flock.Kit.Tests;

public class DumpTimelineTests
{
    private const string Sample = """
        [
         {"id":"4","time":"2026-10-13T12:00:04Z","site":"shop.test","group":"req-2","label":null,
          "source":{"file":"/code/shop/app/Http/Controllers/CartController.php","line":42,"name":"CartController.php"},
          "request":{"method":"GET","uri":"https://shop.test/cart"},"command_line":null,"text":"second","html":"<pre class=sf-dump>second</pre>"},
         {"id":"3","time":"2026-10-13T12:00:03Z","site":"shop.test","group":"req-2","label":"cart",
          "source":{"file":"/code/shop/app/Models/Cart.php","line":7,"name":"Cart.php"},
          "request":{"method":"GET","uri":"https://shop.test/cart"},"command_line":null,"text":"first","html":"<pre class=sf-dump>first</pre>"},
         {"id":"1","time":"2026-10-13T11:59:00Z","site":"","group":"cli-1","source":null,"request":null,
          "command_line":"artisan tinker","text":"<b>old</b>","html":"<pre class=sf-dump>old</pre>"}
        ]
        """;

    private static List<Dump> Dumps() => FlockJson.Decode<List<Dump>>(Sample);

    [Fact]
    public void LatestGroupKeepsOnlyTheNewestRequest()
    {
        Assert.Equal(["4", "3"], DumpTimeline.LatestGroup(Dumps()).Select(d => d.Id));
        Assert.Empty(DumpTimeline.LatestGroup([]));
    }

    [Fact]
    public void SearchMatchesValueSourceAndOrigin()
    {
        Assert.Equal(["1"], DumpTimeline.Filter(Dumps(), "tinker").Select(d => d.Id));
        Assert.Equal(["3"], DumpTimeline.Filter(Dumps(), "CART.php").Select(d => d.Id));
        Assert.Equal(3, DumpTimeline.Filter(Dumps(), " ").Count);
    }

    [Fact]
    public void HtmlShowsRequestsNewestFirstAndDumpsInOrder()
    {
        var html = DumpTimeline.Html(Dumps(), "<script>HEADER</script>", Editor.PhpStorm);

        Assert.Contains("<span class=\"verb get\">GET</span>", html);
        Assert.True(html.IndexOf("https://shop.test/cart", StringComparison.Ordinal) < html.IndexOf("artisan tinker", StringComparison.Ordinal));
        Assert.True(html.IndexOf(">first<", StringComparison.Ordinal) < html.IndexOf(">second<", StringComparison.Ordinal));
        Assert.Contains("phpstorm://open?file=/code/shop/app/Models/Cart.php&amp;line=7", html);
        Assert.Contains(">app/Models/Cart.php:7<", html);
        Assert.Equal(2, html.Split("HEADER").Length);
    }

    [Fact]
    public void EditorUrls()
    {
        Assert.Equal("vscode://file/a%20b/c.php:3", Editor.VsCode.Url("/a b/c.php", 3));
        Assert.Equal("phpstorm://open?file=/x.blade.php", Editor.PhpStorm.Url("/x.blade.php", 0));
        Assert.Null(Editor.None.Url("/x.php", 1));
    }

    [Fact]
    public void EditorUrlsUseForwardSlashesForWindowsPaths()
    {
        Assert.Equal("vscode://file/C:/code/my%20shop/app/x.php:3", Editor.VsCode.Url(@"C:\code\my shop\app\x.php", 3));
        Assert.Equal("phpstorm://open?file=C:/code/x.php&line=9", Editor.PhpStorm.Url(@"C:\code\x.php", 9));
        Assert.Equal("app/Models/Cart.php", DumpTimeline.ShortPath(@"C:\code\shop\app\Models\Cart.php"));
    }

    [Fact]
    public void LegacyBodiesLoseTheirStylesheet()
    {
        var dump = Dumps()[0];
        dump.Html = "<style>pre.sf-dump{color:red}</style><script>Sfdump=1</script><pre class=sf-dump>x</pre>";
        var body = DumpTimeline.CleanedHtml(dump);
        Assert.DoesNotContain("<style", body);
        Assert.Contains("<pre class=sf-dump>x</pre>", body);
    }
}
