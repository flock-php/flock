using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>Grouped list of services with a detail column, like Herd's Services pane.</summary>
internal sealed class ServicesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    private static readonly string[] CategoryOrder = ["Core", "Database", "Cache & Queue", "Mail", "Debugging"];
    private string? selection;

    public override string Title => "Services";
    public override string Subtitle => "Databases, caches and the tools around your sites. Everything listens on 127.0.0.1 only.";

    public override UIElement Build()
    {
        var services = Store.Services;
        if (services.Count == 0)
        {
            return Ui.EmptyState(Ui.GlyphServer, "No services");
        }
        var selected = services.FirstOrDefault(s => s.Id == selection) ?? services[0];

        var list = new StackPanel { Spacing = 4 };
        var present = services.Select(s => s.Category).Distinct().ToList();
        var categories = CategoryOrder.Where(present.Contains).Concat(present.Except(CategoryOrder).Order()).ToList();
        foreach (var category in categories)
        {
            var heading = Ui.Text(category, "BodyStrongTextBlockStyle");
            heading.Margin = new Thickness(1, list.Children.Count == 0 ? 0 : 16, 0, 4);
            list.Children.Add(heading);
            foreach (var service in services.Where(s => s.Category == category))
            {
                list.Children.Add(Row(service, service.Id == selected.Id));
            }
        }

        var grid = new Grid { ColumnSpacing = 16 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(300) });
        var detail = Detail(selected);
        Grid.SetColumn(detail, 1);
        grid.Children.Add(list);
        grid.Children.Add(detail);
        return grid;
    }

    private Button Row(Service service, bool isSelected)
    {
        var info = Ui.Row(12, Ui.StatusDot(service.Running), Ui.Column(1, Ui.Text(service.Name, "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(DetailText(service), wrap: false)));
        var actions = Ui.Row(8);
        if (service.Installed)
        {
            if (service.HasConfigurablePort)
            {
                actions.Children.Add(Ui.Button("Port…", () => _ = EditPort(service)));
            }
            if (service.Stoppable)
            {
                var toggle = Ui.Button(service.Running ? "Stop" : "Start", () =>
                    _ = Store.PerformOnServicesAsync(c => service.Running ? c.StopServiceAsync(service.Id) : c.StartServiceAsync(service.Id)));
                toggle.IsEnabled = !Store.IsBusy;
                actions.Children.Add(toggle);
            }
        }
        else
        {
            actions.Children.Add(Ui.Caption("Not installed", wrap: false));
        }

        // A card that selects the service for the detail column.
        var card = new Button
        {
            Content = Ui.Spread(info, actions),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Padding = new Thickness(16, 10, 12, 10),
            Background = Ui.Brush(isSelected ? "SubtleFillColorSecondaryBrush" : "CardBackgroundFillColorDefaultBrush"),
            BorderBrush = isSelected ? Ui.Accent : Ui.Brush("CardStrokeColorDefaultBrush"),
        };
        card.Click += (_, _) =>
        {
            selection = service.Id;
            Rerender();
        };
        return card;
    }

    private static string DetailText(Service service)
    {
        var parts = new List<string>();
        if (service.Version.Length > 0)
        {
            parts.Add(service.Version);
        }
        if (service.Port > 0)
        {
            parts.Add($"Port {service.Port}");
        }
        if (!service.Running)
        {
            parts.Add("Stopped");
        }
        return string.Join(" · ", parts);
    }

    private static Border Detail(Service service)
    {
        var panel = new StackPanel { Spacing = 14 };
        panel.Children.Add(Ui.Row(8, Ui.StatusDot(service.Running), Ui.Text(service.Name, "SubtitleTextBlockStyle")));

        if (service.Id == "dns" && service.Version.Length == 0 && service.Port == 0)
        {
            panel.Children.Add(Ui.Caption(service.Installed
                ? "Windows can't send a whole domain to Flock, so the Flock hosts helper (a small Windows service) adds every site to the hosts file. Subdomains (api.app.test) are not covered — link them as their own site."
                : "The hosts helper is not installed, so site domains don't resolve. Run `flock install` in a terminal once; Windows asks for permission."));
        }

        if (service.IsDatabase && TablePlus() is { } tablePlus)
        {
            panel.Children.Add(Ui.Spread(Ui.Text("TablePlus", "BodyStrongTextBlockStyle"), Ui.Button("Open", () =>
                System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(tablePlus, service.Url) { UseShellExecute = false }))));
        }
        if (service.Env.Length > 0)
        {
            panel.Children.Add(Ui.Column(6, Ui.Text("Environment variables", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Env)));
        }
        if (service.Log.Length > 0)
        {
            panel.Children.Add(Ui.Spread(Ui.Text("Logs", "BodyStrongTextBlockStyle"), Ui.Button("Open", () => Native.Open(FlockPaths.LogFile(service.Log)))));
        }
        if (service.Url.Length > 0)
        {
            panel.Children.Add(Ui.Column(6, Ui.Text("Connection", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Url)));
        }
        var card = Ui.Card(panel, new Thickness(16));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    private static string? TablePlus()
    {
        foreach (var root in new[] { Environment.SpecialFolder.ProgramFiles, Environment.SpecialFolder.LocalApplicationData })
        {
            var path = Path.Combine(Environment.GetFolderPath(root), "TablePlus", "TablePlus.exe");
            if (File.Exists(path))
            {
                return path;
            }
        }
        return null;
    }

    private async Task EditPort(Service service)
    {
        var box = new NumberBox
        {
            Header = "Port",
            Value = service.Port,
            Minimum = 1024,
            Maximum = 65535,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
        };
        var dialog = new ContentDialog
        {
            Title = $"{service.Name} port",
            Content = Ui.Column(12, box, Ui.Caption("Ports 1024–65535. Update the port in your projects' .env files too.")),
            PrimaryButtonText = "Save",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary || double.IsNaN(box.Value))
        {
            return;
        }
        var port = (int)box.Value;
        if (port != service.Port && port is >= 1024 and <= 65535)
        {
            await Store.PerformOnServicesAsync(c => c.SetServicePortAsync(service.Id, port));
        }
    }
}
