using System.Globalization;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Media;
using Windows.UI;

namespace Flock.Tray;

internal sealed class LogLevelTextConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) =>
        value is string level ? level.ToUpperInvariant() : "";

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

/// <summary>An entry's message: three lines, or all of it while its details are open.</summary>
internal sealed class LogMessageLinesConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) =>
        value is true ? 0 : 3;

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

internal sealed class LogTimeConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) =>
        value is DateTimeOffset time
            ? time.ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture)
            : "";

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

internal sealed class LogLevelBrushConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
    {
        var color = value is string level ? level.ToLowerInvariant() switch
        {
            "debug" => Color.FromArgb(255, 118, 118, 118),
            "info" => Color.FromArgb(255, 0, 95, 184),
            "notice" => Color.FromArgb(255, 0, 128, 128),
            "warning" => Color.FromArgb(255, 157, 93, 0),
            _ => Color.FromArgb(255, 196, 43, 28),
        } : Color.FromArgb(255, 196, 43, 28);
        return new SolidColorBrush(color);
    }

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}
