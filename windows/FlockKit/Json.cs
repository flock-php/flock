using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;

namespace Flock.Kit;

public static partial class FlockJson
{
    /// <summary>
    /// Options for the daemon's JSON: snake_case keys, Go's RFC 3339 times (zero to nine fractional
    /// digits) and Go's nil slices, which arrive as null and are read as empty lists.
    /// </summary>
    public static readonly JsonSerializerOptions Options = Create();

    private static JsonSerializerOptions Create()
    {
        var options = new JsonSerializerOptions
        {
            PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
            DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        };
        options.Converters.Add(new GoTimeConverter());
        options.Converters.Add(new NullableGoTimeConverter());
        options.Converters.Add(new NullAsEmptyListFactory());
        return options;
    }

    public static T Decode<T>(string json) =>
        JsonSerializer.Deserialize<T>(json, Options) ?? throw new JsonException($"null {typeof(T).Name}");

    public static string Encode<T>(T value) => JsonSerializer.Serialize(value, Options);

    /// <summary>Parses RFC 3339 with or without fractional seconds of any precision.</summary>
    public static DateTimeOffset? ParseDate(string value)
    {
        var m = Rfc3339().Match(value);
        if (!m.Success)
        {
            return null;
        }
        // .NET understands at most 7 fractional digits (100 ns ticks); Go sends up to 9.
        var fraction = m.Groups["frac"].Value;
        if (fraction.Length > 7)
        {
            fraction = fraction[..7];
        }
        var normalized = m.Groups["base"].Value + (fraction.Length > 0 ? "." + fraction : "") + m.Groups["zone"].Value;
        var format = fraction.Length > 0 ? "yyyy-MM-dd'T'HH:mm:ss." + new string('F', fraction.Length) + "K" : "yyyy-MM-dd'T'HH:mm:ssK";
        return DateTimeOffset.TryParseExact(normalized, format, CultureInfo.InvariantCulture, DateTimeStyles.None, out var parsed)
            ? parsed
            : null;
    }

    [GeneratedRegex(@"^(?<base>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(?<frac>\d+))?(?<zone>Z|[+-]\d{2}:\d{2})$")]
    private static partial Regex Rfc3339();

    private sealed class GoTimeConverter : JsonConverter<DateTimeOffset>
    {
        public override DateTimeOffset Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        {
            var text = reader.GetString() ?? "";
            return ParseDate(text) ?? throw new JsonException($"Invalid RFC 3339 date: {text}");
        }

        public override void Write(Utf8JsonWriter writer, DateTimeOffset value, JsonSerializerOptions options) =>
            writer.WriteStringValue(value.ToString("yyyy-MM-dd'T'HH:mm:ss.FFFFFFFK", CultureInfo.InvariantCulture));
    }

    private sealed class NullableGoTimeConverter : JsonConverter<DateTimeOffset?>
    {
        public override bool HandleNull => true;

        public override DateTimeOffset? Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        {
            if (reader.TokenType == JsonTokenType.Null)
            {
                return null;
            }
            var text = reader.GetString() ?? "";
            return ParseDate(text) ?? throw new JsonException($"Invalid RFC 3339 date: {text}");
        }

        public override void Write(Utf8JsonWriter writer, DateTimeOffset? value, JsonSerializerOptions options)
        {
            if (value is { } v)
            {
                writer.WriteStringValue(v.ToString("yyyy-MM-dd'T'HH:mm:ss.FFFFFFFK", CultureInfo.InvariantCulture));
            }
            else
            {
                writer.WriteNullValue();
            }
        }
    }

    private sealed class NullAsEmptyListFactory : JsonConverterFactory
    {
        public override bool CanConvert(Type typeToConvert) =>
            typeToConvert.IsGenericType && typeToConvert.GetGenericTypeDefinition() == typeof(List<>);

        public override JsonConverter CreateConverter(Type typeToConvert, JsonSerializerOptions options) =>
            (JsonConverter)Activator.CreateInstance(typeof(NullAsEmptyList<>).MakeGenericType(typeToConvert.GetGenericArguments()[0]))!;
    }

    private sealed class NullAsEmptyList<T> : JsonConverter<List<T>>
    {
        public override bool HandleNull => true;

        public override List<T> Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        {
            if (reader.TokenType == JsonTokenType.Null)
            {
                return [];
            }
            if (reader.TokenType != JsonTokenType.StartArray)
            {
                throw new JsonException("expected an array");
            }
            var list = new List<T>();
            while (reader.Read() && reader.TokenType != JsonTokenType.EndArray)
            {
                list.Add(JsonSerializer.Deserialize<T>(ref reader, options)!);
            }
            return list;
        }

        public override void Write(Utf8JsonWriter writer, List<T> value, JsonSerializerOptions options)
        {
            writer.WriteStartArray();
            foreach (var item in value)
            {
                JsonSerializer.Serialize(writer, item, options);
            }
            writer.WriteEndArray();
        }
    }
}