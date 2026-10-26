namespace Flock.Tray.Infrastructure;

/// <summary>Updates keyed rows without clearing the collection or changing retained row objects.</summary>
public sealed class CollectionSynchronizer<TKey, TRow> where TKey : notnull
{
    public void Synchronize<TValue>(IList<TRow> rows, IEnumerable<TValue> values, Func<TRow, TKey> rowKey,
        Func<TValue, TKey> valueKey, Func<TValue, TRow> create, Action<TRow, TValue> update)
    {
        var incoming = values.ToList();
        var wanted = incoming.Select(valueKey).ToHashSet();
        for (var index = rows.Count - 1; index >= 0; index--)
        {
            if (!wanted.Contains(rowKey(rows[index])))
            {
                rows.RemoveAt(index);
            }
        }
        var lookup = rows.ToDictionary(rowKey);
        for (var index = 0; index < incoming.Count; index++)
        {
            var value = incoming[index];
            var key = valueKey(value);
            if (!lookup.TryGetValue(key, out var row))
            {
                row = create(value);
                rows.Insert(index, row);
                lookup[key] = row;
            }
            else
            {
                update(row, value);
                var currentIndex = rows.IndexOf(row);
                if (currentIndex != index)
                {
                    rows.RemoveAt(currentIndex);
                    rows.Insert(index, row);
                }
            }
        }

    }

    public void Synchronize(IList<TRow> rows, IEnumerable<TRow> values, Func<TRow, TKey> key,
        Action<TRow, TRow> update)
    {
        ArgumentNullException.ThrowIfNull(rows);
        ArgumentNullException.ThrowIfNull(values);
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(update);

        var incoming = values.ToList();
        var wanted = incoming.Select(key).ToHashSet();
        for (var index = rows.Count - 1; index >= 0; index--)
        {
            if (!wanted.Contains(key(rows[index])))
            {
                rows.RemoveAt(index);
            }
        }

        var lookup = rows.ToDictionary(key);
        for (var index = 0; index < incoming.Count; index++)
        {
            var value = incoming[index];
            var valueKey = key(value);
            if (!lookup.TryGetValue(valueKey, out var row))
            {
                row = value;
                rows.Insert(index, row);
                lookup[valueKey] = row;
            }
            else
            {
                update(row, value);
                var currentIndex = rows.IndexOf(row);
                if (currentIndex != index)
                {
                    rows.RemoveAt(currentIndex);
                    rows.Insert(index, row);
                }
            }
        }
    }
}