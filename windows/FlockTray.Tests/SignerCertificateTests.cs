using System.Buffers.Binary;
using System.Formats.Asn1;
using System.Reflection.PortableExecutable;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Flock.Tray.Features.Update;

namespace Flock.Tray.Tests;

public sealed class SignerCertificateTests
{
    [Fact]
    public void PublisherFollowsSignerRatherThanCertificateStoreOrder()
    {
        using var firstKey = RSA.Create(2048);
        using var secondKey = RSA.Create(2048);
        using var first = Certificate("Guava s.r.o.", firstKey);
        using var second = Certificate("Other publisher", secondKey);
        var directory = Directory.CreateTempSubdirectory("flock-signers-");
        try
        {
            // Both files contain exactly the same certificates, but different signers.
            // Selecting the first stored certificate cannot return the right publisher for both.
            var firstFile = Path.Combine(directory.FullName, "first.exe");
            var secondFile = Path.Combine(directory.FullName, "second.exe");
            WriteSignedFile(firstFile, first, firstKey, first, second);
            WriteSignedFile(secondFile, second, secondKey, first, second);

            Assert.Equal("Guava s.r.o.", WindowsAuthenticodeVerifier.ReadPublisher(firstFile));
            Assert.Equal("Other publisher", WindowsAuthenticodeVerifier.ReadPublisher(secondFile));
            // Extracting a publisher must not allow a self-signed test file through the trust gate.
            var result = new WindowsAuthenticodeVerifier().Verify(firstFile);
            Assert.NotEqual(0, result.TrustCode);
            Assert.Null(result.Publisher);
        }
        finally
        {
            directory.Delete(recursive: true);
        }
    }

    private static X509Certificate2 Certificate(string organization, RSA key) =>
        new CertificateRequest($"CN=Test signer, O={organization}", key, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1)
            .CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-1), DateTimeOffset.UtcNow.AddDays(1));

    private static void WriteSignedFile(string path, X509Certificate2 signer, RSA key, params X509Certificate2[] certificates)
    {
        // A PKCS#7 envelope in a PE certificate table exercises Windows' embedded-signature reader.
        // Its payload is test data, not an Authenticode image digest, so it cannot pass WinVerifyTrust.
        byte[] content = [1, 2, 3];
        var explicitTag = new Asn1Tag(TagClass.ContextSpecific, 0, isConstructed: true);
        var writer = new AsnWriter(AsnEncodingRules.DER);
        writer.PushSequence();
        writer.WriteObjectIdentifier("1.2.840.113549.1.7.2");
        writer.PushSequence(explicitTag);
        writer.PushSequence();
        writer.WriteInteger(1);
        writer.PushSetOf();
        Algorithm(writer, "2.16.840.1.101.3.4.2.1");
        writer.PopSetOf();
        writer.PushSequence();
        writer.WriteObjectIdentifier("1.2.840.113549.1.7.1");
        writer.PushSequence(explicitTag);
        writer.WriteOctetString(content);
        writer.PopSequence(explicitTag);
        writer.PopSequence();
        writer.PushSetOf(explicitTag);
        foreach (var certificate in certificates)
        {
            writer.WriteEncodedValue(certificate.RawData);
        }
        writer.PopSetOf(explicitTag);
        writer.PushSetOf();
        writer.PushSequence();
        writer.WriteInteger(1);
        writer.PushSequence();
        writer.WriteEncodedValue(signer.IssuerName.RawData);
        writer.WriteIntegerUnsigned(signer.GetSerialNumber().Reverse().ToArray());
        writer.PopSequence();
        Algorithm(writer, "2.16.840.1.101.3.4.2.1");
        Algorithm(writer, "1.2.840.113549.1.1.1");
        writer.WriteOctetString(key.SignData(content, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1));
        writer.PopSequence();
        writer.PopSetOf();
        writer.PopSequence();
        writer.PopSequence(explicitTag);
        writer.PopSequence();
        var signature = writer.Encode();

        var image = File.ReadAllBytes(typeof(SignerCertificateTests).Assembly.Location);
        using var reader = new PEReader(new MemoryStream(image));
        var headers = reader.PEHeaders;
        var tableEntry = headers.PEHeaderStartOffset + (headers.PEHeader!.Magic == PEMagic.PE32Plus ? 144 : 128);
        var offset = (image.Length + 7) & ~7;
        var length = (signature.Length + 8 + 7) & ~7;
        Array.Resize(ref image, offset + length);
        BinaryPrimitives.WriteInt32LittleEndian(image.AsSpan(tableEntry), offset);
        BinaryPrimitives.WriteInt32LittleEndian(image.AsSpan(tableEntry + 4), length);
        BinaryPrimitives.WriteInt32LittleEndian(image.AsSpan(offset), length);
        BinaryPrimitives.WriteUInt16LittleEndian(image.AsSpan(offset + 4), 0x0200);
        BinaryPrimitives.WriteUInt16LittleEndian(image.AsSpan(offset + 6), 0x0002);
        signature.CopyTo(image, offset + 8);
        File.WriteAllBytes(path, image);
    }

    private static void Algorithm(AsnWriter writer, string oid)
    {
        writer.PushSequence();
        writer.WriteObjectIdentifier(oid);
        writer.WriteNull();
        writer.PopSequence();
    }
}
