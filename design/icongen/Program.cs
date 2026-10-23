// Renders the app and tray/menu bar icons of both apps from the brand SVGs in design/logo
// (copied from the website's design/logo). Run from the repository root after changing them:
//
//     dotnet run --project design/icongen
//
// Windows: windows/FlockTray/Assets/AppIcon.ico and Tray{,Stopped,Warning}.{Light,Dark}.ico, the
// notification area icon for a light or dark taskbar, plus large .png copies for inside the
// windows. macOS: macos/Resources/AppIcon.icns and MenuBar{,Stopped,Warning}{,@2x}.png, template
// images that macOS tints for the menu bar itself.
using SkiaSharp;
using Svg.Skia;

var root = Directory.GetCurrentDirectory();
if (!Directory.Exists(Path.Combine(root, "design", "logo")))
{
    Console.Error.WriteLine("run from the repository root");
    return 1;
}
var appIcon = File.ReadAllText(Path.Combine(root, "design", "logo", "flock-app-icon.svg"));
var logomark = File.ReadAllText(Path.Combine(root, "design", "logo", "flock-logomark-black.svg"));
// The brand's logomark versions by background: teal on light, mint on dark.
var trayMarks = new Dictionary<string, string>
{
    ["Light"] = File.ReadAllText(Path.Combine(root, "design", "logo", "flock-logomark-light.svg")),
    ["Dark"] = File.ReadAllText(Path.Combine(root, "design", "logo", "flock-logomark-dark.svg")),
};
// Ember, the brand's "notice this now" color, for the warning badge.
var ember = SKColor.Parse("#E8792B");
var windowsAssets = Path.Combine(root, "windows", "FlockTray", "Assets");
var macResources = Path.Combine(root, "macos", "Resources");
Directory.CreateDirectory(macResources);

// Windows app icon: the full tile, which is how Windows 11 shows app icons.
WriteIco(Path.Combine(windowsAssets, "AppIcon.ico"), [16, 20, 24, 32, 40, 48, 64, 96, 128, 256],
    size => Render(appIcon, size, size, 0));

// Notification area: the light (teal) or dark (mint) logomark for a light or dark taskbar, at
// every small-icon size from 100% to 300% scaling. Stopped is dimmed; a warning adds an ember
// badge.
int[] traySizes = [16, 20, 24, 28, 32, 40, 48, 64];
foreach (var (theme, mark) in trayMarks)
{
    WriteIco(Path.Combine(windowsAssets, $"Tray.{theme}.ico"), traySizes, size => Mark(mark, size, 1));
    WriteIco(Path.Combine(windowsAssets, $"TrayStopped.{theme}.ico"), traySizes, size => Mark(mark, size, 0.45f));
    WriteIco(Path.Combine(windowsAssets, $"TrayWarning.{theme}.ico"), traySizes, size => Badge(Mark(mark, size, 1), ember));

    // Inside the app's windows (the panel's header): WinUI's Image shows an .ico's first, smallest
    // image scaled up, so the windows get large PNGs instead (128 px: 32 points at 400%).
    File.WriteAllBytes(Path.Combine(windowsAssets, $"Tray.{theme}.png"), Png(Mark(mark, 128, 1)));
    File.WriteAllBytes(Path.Combine(windowsAssets, $"TrayStopped.{theme}.png"), Png(Mark(mark, 128, 0.45f)));
    File.WriteAllBytes(Path.Combine(windowsAssets, $"TrayWarning.{theme}.png"), Png(Badge(Mark(mark, 128, 1), ember)));
}
// The app icon in the windows (title bar, Setup, About): up to 72 points at 300%.
File.WriteAllBytes(Path.Combine(windowsAssets, "AppIcon.png"), Png(Render(appIcon, 256, 256, 0)));
// The same tile in the middle of a share's QR code (internal/share/qr.go).
File.WriteAllBytes(Path.Combine(root, "internal", "share", "logo.png"), Png(Render(appIcon, 256, 256, 0)));

// macOS app icon: the tile on Apple's icon grid (824 of 1024 points, with a soft shadow).
WriteIcns(Path.Combine(macResources, "AppIcon.icns"), size =>
{
    var bitmap = new SKBitmap(size, size, SKColorType.Rgba8888, SKAlphaType.Premul);
    using var canvas = new SKCanvas(bitmap);
    canvas.Clear(SKColors.Transparent);
    var scale = size / 1024f;
    var tile = Render(appIcon, (int)Math.Round(824 * scale), (int)Math.Round(824 * scale), 0);
    using var shadow = new SKPaint { ImageFilter = SKImageFilter.CreateDropShadow(0, 10 * scale, 12 * scale, 12 * scale, new SKColor(0, 0, 0, 77)) };
    using var image = SKImage.FromBitmap(tile);
    canvas.DrawImage(image, 100 * scale, 100 * scale, SKSamplingOptions.Default, shadow);
    return bitmap;
});

// Menu bar: 18-point template images; macOS colors them for the menu bar's appearance.
foreach (var (name, alpha, badge) in new[] { ("MenuBar", 1f, false), ("MenuBarStopped", 0.45f, false), ("MenuBarWarning", 1f, true) })
{
    foreach (var (suffix, size) in new[] { ("", 18), ("@2x", 36) })
    {
        var image = Mark(logomark, size, alpha);
        if (badge)
        {
            image = Badge(image, SKColors.Black);
        }
        File.WriteAllBytes(Path.Combine(macResources, $"{name}{suffix}.png"), Png(image));
    }
    // Inside the app's windows (panel header, Settings): large enough for 64 points at 2x.
    var large = Mark(logomark, 128, alpha);
    File.WriteAllBytes(Path.Combine(macResources, name.Replace("MenuBar", "LogoMark") + ".png"), Png(badge ? Badge(large, SKColors.Black) : large));
}

Console.WriteLine("icons written");

// `-- --preview <file.png>`: every tray and menu bar icon at 1x on a light and a dark taskbar,
// enlarged 4× without smoothing, to check them by eye.
if (args is ["--preview", var previewPath])
{
    var rows = new List<(SKColor Background, SKBitmap[] Icons)>();
    foreach (var (theme, background) in new[] { ("Light", "#F3F3F3"), ("Dark", "#202020") })
    {
        var mark = trayMarks[theme];
        foreach (var size in new[] { 16, 24, 32 })
        {
            rows.Add((SKColor.Parse(background), [Mark(mark, size, 1), Mark(mark, size, 0.45f), Badge(Mark(mark, size, 1), ember)]));
        }
    }
    foreach (var (background, tint) in new[] { ("#ECECEC", "#000000"), ("#2A2A2A", "#FFFFFF") })
    {
        var mark = logomark.Replace("#000000", tint);
        rows.Add((SKColor.Parse(background), [Mark(mark, 18, 1), Mark(mark, 18, 0.45f), Badge(Mark(mark, 18, 1), SKColor.Parse(tint))]));
    }
    rows.Add((SKColors.White, [Render(appIcon, 16, 16, 0), Render(appIcon, 32, 32, 0), Render(appIcon, 48, 48, 0)]));

    const int zoom = 4, cell = 48 * zoom + 16;
    using var sheet = new SKBitmap(3 * cell, rows.Count * cell);
    using var canvas = new SKCanvas(sheet);
    for (var r = 0; r < rows.Count; r++)
    {
        using var fill = new SKPaint { Color = rows[r].Background };
        canvas.DrawRect(0, r * cell, 3 * cell, cell, fill);
        for (var c = 0; c < rows[r].Icons.Length; c++)
        {
            var icon = rows[r].Icons[c];
            using var image = SKImage.FromBitmap(icon);
            canvas.DrawImage(image, SKRect.Create(c * cell + 8, r * cell + 8, icon.Width * zoom, icon.Height * zoom), new SKSamplingOptions(SKFilterMode.Nearest));
        }
    }
    File.WriteAllBytes(previewPath, Png(sheet));
}
return 0;

// Renders an SVG into a width × height bitmap, fitted and centered, with an inset on every side.
static SKBitmap Render(string svgText, int width, int height, float inset, float alpha = 1)
{
    using var svg = new SKSvg();
    var picture = svg.FromSvg(svgText) ?? throw new InvalidOperationException("unreadable SVG");
    var bounds = picture.CullRect;
    var scale = Math.Min((width - 2 * inset) / bounds.Width, (height - 2 * inset) / bounds.Height);
    var bitmap = new SKBitmap(width, height, SKColorType.Rgba8888, SKAlphaType.Premul);
    using var canvas = new SKCanvas(bitmap);
    canvas.Clear(SKColors.Transparent);
    canvas.Translate((width - bounds.Width * scale) / 2, (height - bounds.Height * scale) / 2);
    canvas.Scale(scale);
    canvas.Translate(-bounds.Left, -bounds.Top);
    using var paint = new SKPaint { Color = SKColors.Black.WithAlpha((byte)(alpha * 255)) };
    canvas.DrawPicture(picture, paint);
    return bitmap;
}

// The logomark in a square icon, with a little room at small sizes so it doesn't touch the edge.
static SKBitmap Mark(string svgText, int size, float alpha) => Render(svgText, size, size, size <= 20 ? 0.5f : size / 32f, alpha);

// A status dot in the bottom right corner, cut free of the mark beneath it.
static SKBitmap Badge(SKBitmap bitmap, SKColor color)
{
    var size = bitmap.Width;
    var radius = size * 0.22f;
    var center = new SKPoint(size - radius - size * 0.02f, size - radius - size * 0.02f);
    using var canvas = new SKCanvas(bitmap);
    using var clear = new SKPaint { BlendMode = SKBlendMode.Clear, IsAntialias = true };
    canvas.DrawCircle(center, radius + Math.Max(1, size * 0.07f), clear);
    using var dot = new SKPaint { Color = color, IsAntialias = true };
    canvas.DrawCircle(center, radius, dot);
    return bitmap;
}

static byte[] Png(SKBitmap bitmap)
{
    using var image = SKImage.FromBitmap(bitmap);
    using var data = image.Encode(SKEncodedImageFormat.Png, 100);
    return data.ToArray();
}

// An .ico with a PNG image per size (supported since Windows Vista).
static void WriteIco(string path, int[] sizes, Func<int, SKBitmap> render)
{
    var images = sizes.Select(size => Png(render(size))).ToArray();
    using var stream = File.Create(path);
    using var writer = new BinaryWriter(stream);
    writer.Write((short)0);
    writer.Write((short)1);
    writer.Write((short)sizes.Length);
    var offset = 6 + 16 * sizes.Length;
    for (var i = 0; i < sizes.Length; i++)
    {
        writer.Write((byte)(sizes[i] >= 256 ? 0 : sizes[i]));
        writer.Write((byte)(sizes[i] >= 256 ? 0 : sizes[i]));
        writer.Write((byte)0);
        writer.Write((byte)0);
        writer.Write((short)1);
        writer.Write((short)32);
        writer.Write(images[i].Length);
        writer.Write(offset);
        offset += images[i].Length;
    }
    foreach (var image in images)
    {
        writer.Write(image);
    }
}

// An .icns with PNG images for every size macOS asks for (16 to 512 points, 1x and 2x).
static void WriteIcns(string path, Func<int, SKBitmap> render)
{
    (string Type, int Size)[] entries =
    [
        ("icp4", 16), ("ic11", 32), ("icp5", 32), ("ic12", 64), ("ic07", 128),
        ("ic13", 256), ("ic08", 256), ("ic14", 512), ("ic09", 512), ("ic10", 1024),
    ];
    var chunks = entries.Select(e => (e.Type, Data: Png(render(e.Size)))).ToArray();
    using var stream = File.Create(path);
    stream.Write("icns"u8);
    WriteBigEndian(stream, 8 + chunks.Sum(c => 8 + c.Data.Length));
    foreach (var (type, data) in chunks)
    {
        stream.Write(System.Text.Encoding.ASCII.GetBytes(type));
        WriteBigEndian(stream, 8 + data.Length);
        stream.Write(data);
    }
}

static void WriteBigEndian(Stream stream, int value)
{
    Span<byte> bytes = stackalloc byte[4];
    System.Buffers.Binary.BinaryPrimitives.WriteInt32BigEndian(bytes, value);
    stream.Write(bytes);
}
