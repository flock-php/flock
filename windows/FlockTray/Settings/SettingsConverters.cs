using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray;

internal sealed class RunningTextConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) => value is true ? "Running" : "Stopped";
    public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
}

internal sealed class StatusBrushConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) => value is true ? Ui.Success : Ui.Brush("ControlStrongFillColorDisabledBrush");
    public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
}

internal sealed class StringVisibilityConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) => value is string { Length: > 0 } ? Visibility.Visible : Visibility.Collapsed;
    public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
}

internal sealed class InverseBooleanVisibilityConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) => value is true ? Visibility.Collapsed : Visibility.Visible;
    public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
}
