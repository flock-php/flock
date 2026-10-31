using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class CollectionSynchronizerTests
{
    private sealed class Row(string id, string text)
    {
        public string Id { get; } = id;
        public string Text { get; set; } = text;
    }

    [Fact]
    public void SynchronizePreservesRowsDraftsAndOrder()
    {
        var first = new Row("a", "draft");
        var second = new Row("b", "old");
        var rows = new List<Row> { first, second };
        var synchronizer = new CollectionSynchronizer<string, Row>();

        synchronizer.Synchronize(rows, [new Row("b", "new"), new Row("a", "server")], row => row.Id,
            (row, value) => row.Text = value.Text == "server" && row.Id == "a" ? row.Text : value.Text);

        Assert.Same(first, rows[1]);
        Assert.Same(second, rows[0]);
        Assert.Equal("draft", first.Text);
        Assert.Equal("new", second.Text);
    }

    [Fact]
    public void SynchronizeRemovesMissingRowsAndInsertsNewRows()
    {
        var retained = new Row("a", "draft");
        var removed = new Row("b", "old");
        var rows = new List<Row> { retained, removed };
        var synchronizer = new CollectionSynchronizer<string, Row>();

        synchronizer.Synchronize(rows, [new Row("c", "new"), new Row("a", "server")], row => row.Id,
            (row, value) => row.Text = value.Text);

        Assert.Equal(["c", "a"], rows.Select(row => row.Id));
        Assert.Same(retained, rows[1]);
        Assert.DoesNotContain(removed, rows);
        Assert.Equal("server", retained.Text);
    }
}