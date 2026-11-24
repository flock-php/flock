using System.Text;

namespace Flock.Kit;

/// <summary>
/// Answers DNS queries the way the daemon's DNS server does (internal/dns): an A record of
/// 127.0.0.1 for every name under the TLD, no records for its other types (AAAA included: sites
/// listen on IPv4), and REFUSED for names outside it. The app serves this on Windows for a daemon
/// in WSL, whose own port 53 Windows can't reach: WSL keeps that port for its DNS.
/// </summary>
public static class WildcardDns
{
    private const int HeaderLength = 12;
    private const ushort TypeA = 1;
    private const ushort ClassInternet = 1;
    private const uint Ttl = 60;
    private const byte FormatError = 1;
    private const byte Refused = 5;

    /// <summary>The answer to a query, or null when it isn't one worth answering (too short, a response).</summary>
    public static byte[]? Answer(ReadOnlySpan<byte> query, string tld)
    {
        if (query.Length < HeaderLength || (query[2] & 0x80) != 0)
        {
            return null;
        }
        var questions = (query[4] << 8) | query[5];
        var opcode = (query[2] >> 3) & 0x0F;
        if (questions != 1 || opcode != 0 || !TryReadQuestion(query, out var name, out var type, out var @class, out var end))
        {
            return Reply(query[..HeaderLength], [], FormatError, answer: false);
        }
        var question = query[HeaderLength..end];
        if (!Owns(name, tld))
        {
            return Reply(query[..HeaderLength], question, Refused, answer: false);
        }
        return Reply(query[..HeaderLength], question, 0, answer: type == TypeA && @class == ClassInternet);
    }

    private static bool Owns(string name, string tld)
    {
        tld = tld.Trim('.');
        return tld.Length > 0
            && (name.Equals(tld, StringComparison.OrdinalIgnoreCase) || name.EndsWith("." + tld, StringComparison.OrdinalIgnoreCase));
    }

    /// <summary>Reads the one question after the header. Queries don't compress names, so a pointer is an error.</summary>
    private static bool TryReadQuestion(ReadOnlySpan<byte> query, out string name, out ushort type, out ushort @class, out int end)
    {
        name = "";
        type = @class = 0;
        end = 0;
        var labels = new StringBuilder();
        var at = HeaderLength;
        while (true)
        {
            if (at >= query.Length)
            {
                return false;
            }
            int length = query[at++];
            if (length == 0)
            {
                break;
            }
            if (length > 63 || at + length > query.Length)
            {
                return false;
            }
            if (labels.Length > 0)
            {
                labels.Append('.');
            }
            labels.Append(Encoding.ASCII.GetString(query.Slice(at, length)));
            at += length;
        }
        if (at + 4 > query.Length)
        {
            return false;
        }
        name = labels.ToString();
        type = (ushort)((query[at] << 8) | query[at + 1]);
        @class = (ushort)((query[at + 2] << 8) | query[at + 3]);
        end = at + 4;
        return true;
    }

    private static byte[] Reply(ReadOnlySpan<byte> header, ReadOnlySpan<byte> question, byte rcode, bool answer)
    {
        var reply = new byte[HeaderLength + question.Length + (answer ? 16 : 0)];
        reply[0] = header[0];
        reply[1] = header[1];
        // A response (QR), authoritative (AA), keeping the query's opcode and recursion-desired bit.
        reply[2] = (byte)(0x84 | (header[2] & 0x79));
        reply[3] = rcode;
        reply[5] = (byte)(question.Length > 0 ? 1 : 0);
        reply[7] = (byte)(answer ? 1 : 0);
        question.CopyTo(reply.AsSpan(HeaderLength));
        if (answer)
        {
            var record = reply.AsSpan(HeaderLength + question.Length);
            record[0] = 0xC0; // the name: a pointer to the question's, at offset 12
            record[1] = HeaderLength;
            record[3] = (byte)TypeA;
            record[5] = (byte)ClassInternet;
            record[6] = (byte)(Ttl >> 24);
            record[7] = (byte)(Ttl >> 16);
            record[8] = (byte)(Ttl >> 8);
            record[9] = (byte)Ttl;
            record[11] = 4;
            record[12] = 127;
            record[15] = 1;
        }
        return reply;
    }
}
