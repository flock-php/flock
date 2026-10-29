namespace Flock.Kit.Tests;

public class ConflictsTests
{
    [Fact]
    public void DecodesAndGroupsConflicts()
    {
        var list = FlockJson.Decode<ConflictList>("""
            {"conflicts":[
              {"port":80,"service":"HTTP","holder":"Herd (nginx.exe, pid 812)","app":"Herd","hint":"quit Herd","message":"port 80 (HTTP) is used by Herd"},
              {"port":2525,"service":"mail","holder":"Mailpit (mailpit.exe, pid 9)","app":"Mailpit","hint":"quit Mailpit","message":"…"},
              {"port":443,"service":"HTTPS","holder":"Herd (nginx.exe, pid 812)","app":"Herd","hint":"quit Herd","message":"…"},
              {"port":53531,"service":"app API","holder":"another program","app":"the other program","message":"…"}
            ]}
            """);

        var apps = ConflictingApp.Group(list.Conflicts);
        Assert.Equal(["Herd", "Mailpit", "the other program"], apps.Select(a => a.App));
        Assert.Equal("Quit Herd", apps[0].Title);
        Assert.Equal("Herd (nginx.exe, pid 812) uses port 80 (HTTP) and port 443 (HTTPS).", apps[0].Detail);
        Assert.Equal("Quit the other program", apps[2].Title);
        Assert.Equal("Another program uses port 53531 (app API).", apps[2].Detail);
    }

    [Fact]
    public void DecodesNoConflicts()
    {
        Assert.Empty(FlockJson.Decode<ConflictList>("""{"conflicts":[]}""").Conflicts);
    }
}
