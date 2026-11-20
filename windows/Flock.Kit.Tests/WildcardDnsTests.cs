namespace Flock.Kit.Tests;

public class WildcardDnsTests
{
    private const ushort A = 1;
    private const ushort Aaaa = 28;

    private static byte[] Query(string name, ushort type = A, ushort id = 0x1234)
    {
        var bytes = new List<byte> { (byte)(id >> 8), (byte)id, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0 };
        foreach (var label in name.Split('.', StringSplitOptions.RemoveEmptyEntries))
        {
            bytes.Add((byte)label.Length);
            bytes.AddRange(System.Text.Encoding.ASCII.GetBytes(label));
        }
        bytes.AddRange([0, (byte)(type >> 8), (byte)type, 0, 1]);
        return [.. bytes];
    }

    private static int Rcode(byte[] reply) => reply[3] & 0x0F;
    private static int Answers(byte[] reply) => (reply[6] << 8) | reply[7];

    [Theory]
    [InlineData("app.test")]
    [InlineData("a.b.c.d.app.test")]
    [InlineData("APP.TEST")]
    [InlineData("test")]
    public void NamesUnderTheTldAreLoopback(string name)
    {
        var query = Query(name);

        var reply = WildcardDns.Answer(query, "test")!;

        Assert.Equal(0, Rcode(reply));
        Assert.Equal(1, Answers(reply));
        Assert.Equal([127, 0, 0, 1], reply[^4..]);
        Assert.Equal(query[..2], reply[..2]);
        Assert.Equal(0x85, reply[2]); // response, authoritative, recursion desired as asked
        Assert.Equal(query[12..], reply[12..query.Length]);
    }

    [Fact]
    public void OtherTypesGetNoRecords()
    {
        var reply = WildcardDns.Answer(Query("app.test", Aaaa), "test")!;

        Assert.Equal(0, Rcode(reply));
        Assert.Equal(0, Answers(reply));
    }

    [Theory]
    [InlineData("example.com")]
    [InlineData("apptest")]
    [InlineData("app.test.com")]
    public void NamesOutsideTheTldAreRefused(string name)
    {
        var reply = WildcardDns.Answer(Query(name), "test")!;

        Assert.Equal(5, Rcode(reply));
        Assert.Equal(0, Answers(reply));
    }

    [Fact]
    public void TheTldMayBeWrittenWithDots()
    {
        Assert.Equal(1, Answers(WildcardDns.Answer(Query("app.test"), ".test.")!));
        Assert.Equal(5, Rcode(WildcardDns.Answer(Query("app.test"), "")!));
    }

    [Fact]
    public void AnEdnsRecordAfterTheQuestionIsIgnored()
    {
        byte[] opt = [0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0, 0];
        byte[] query = [.. Query("app.test"), .. opt];
        query[11] = 1;

        var reply = WildcardDns.Answer(query, "test")!;

        Assert.Equal(1, Answers(reply));
        Assert.Equal(0, reply[11]);
    }

    [Fact]
    public void BrokenQueriesAreFormatErrorsOrDropped()
    {
        var truncated = Query("app.test")[..^3];
        var pointer = Query("app.test");
        pointer[12] = 0xC0;
        var response = Query("app.test");
        response[2] |= 0x80;

        Assert.Equal(1, Rcode(WildcardDns.Answer(truncated, "test")!));
        Assert.Equal(1, Rcode(WildcardDns.Answer(pointer, "test")!));
        Assert.Null(WildcardDns.Answer(response, "test"));
        Assert.Null(WildcardDns.Answer(new byte[5], "test"));
    }
}
