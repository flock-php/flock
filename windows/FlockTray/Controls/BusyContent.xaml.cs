using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>A button's content that shows a spinner and <see cref="BusyText" /> while <see cref="IsBusy" />.</summary>
public sealed partial class BusyContent : UserControl
{
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(
        nameof(Text),
        typeof(string),
        typeof(BusyContent),
        new PropertyMetadata("", (control, _) => ((BusyContent)control).Apply()));

    public static readonly DependencyProperty BusyTextProperty = DependencyProperty.Register(
        nameof(BusyText),
        typeof(string),
        typeof(BusyContent),
        new PropertyMetadata("", (control, _) => ((BusyContent)control).Apply()));

    public static readonly DependencyProperty IsBusyProperty = DependencyProperty.Register(
        nameof(IsBusy),
        typeof(bool),
        typeof(BusyContent),
        new PropertyMetadata(false, (control, _) => ((BusyContent)control).Apply()));

    public BusyContent()
    {
        InitializeComponent();
    }

    public string Text
    {
        get => (string)GetValue(TextProperty);
        set => SetValue(TextProperty, value);
    }

    public string BusyText
    {
        get => (string)GetValue(BusyTextProperty);
        set => SetValue(BusyTextProperty, value);
    }

    public bool IsBusy
    {
        get => (bool)GetValue(IsBusyProperty);
        set => SetValue(IsBusyProperty, value);
    }

    private void Apply()
    {
        Ring.IsActive = IsBusy;
        Ring.Visibility = IsBusy
            ? Visibility.Visible
            : Visibility.Collapsed;
        Label.Text = IsBusy && BusyText.Length > 0
            ? BusyText
            : Text;
    }
}