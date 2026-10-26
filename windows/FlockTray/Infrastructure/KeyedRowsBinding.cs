using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Infrastructure;

/// <summary>Binds retained row controls directly to a panel, without mixing ItemsSource and Items.</summary>
public sealed class KeyedRowsBinding<TKey, TRow> where TKey : notnull
{
    private readonly Panel panel;
    private readonly Func<TRow, TKey> key;
    private readonly Func<TRow, UIElement> create;
    private readonly Action<TRow, UIElement>? release;
    private readonly Dictionary<TKey, (TRow Row, UIElement Element)> entries = [];

    public KeyedRowsBinding(Panel panel, Func<TRow, TKey> key, Func<TRow, UIElement> create, Action<TRow, UIElement>? release = null)
    {
        this.panel = panel;
        this.key = key;
        this.create = create;
        this.release = release;
    }

    public void Synchronize(IEnumerable<TRow> rows)
    {
        var values = rows.ToList();
        var wanted = values.Select(key).ToHashSet();
        foreach (var removed in entries.Keys.Where(existing => !wanted.Contains(existing)).ToList())
        {
            var element = entries[removed].Element;
            panel.Children.Remove(element);
            release?.Invoke(entries[removed].Row, element);
            entries.Remove(removed);
        }

        for (var index = 0; index < values.Count; index++)
        {
            var row = values[index];
            var rowKey = key(row);
            if (!entries.TryGetValue(rowKey, out var entry))
            {
                entry = (row, create(row));
                entries[rowKey] = entry;
                panel.Children.Insert(Math.Min(index, panel.Children.Count), entry.Element);
            }
            else
            {
                entry.Row = row;
                var currentIndex = panel.Children.IndexOf(entry.Element);
                if (currentIndex != index)
                {
                    panel.Children.RemoveAt(currentIndex);
                    panel.Children.Insert(Math.Min(index, panel.Children.Count), entry.Element);
                }
            }
        }
    }

    public void Release()
    {
        foreach (var entry in entries.Values)
        {
            panel.Children.Remove(entry.Element);
            release?.Invoke(entry.Row, entry.Element);
        }
        entries.Clear();
    }
}