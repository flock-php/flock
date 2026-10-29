using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class HtmlPageFilesTests
{
    [Fact]
    public async Task ReleaseRemovesOwnedPagesAndIsIdempotent()
    {
        var root = Directory.CreateTempSubdirectory("flock-html-");
        try
        {
            await using var pages = new HtmlPageFiles(root.FullName);
            var file = await pages.WriteAsync(new string('x', 1_500_001));

            Assert.True(File.Exists(file));
            await pages.ReleaseAsync();
            await pages.ReleaseAsync();

            Assert.False(File.Exists(file));
            Assert.False(Directory.Exists(pages.DirectoryPath));
        }
        finally
        {
            if (root.Exists)
            {
                root.Delete(recursive: true);
            }
        }
    }

    [Fact]
    public async Task OneInstanceDoesNotCleanAnotherInstancesDirectory()
    {
        var root = Directory.CreateTempSubdirectory("flock-html-");
        try
        {
            await using var first = new HtmlPageFiles(root.FullName);
            await using var second = new HtmlPageFiles(root.FullName);
            var file = await second.WriteAsync("<p>second</p>");

            await first.ReleaseAsync();

            Assert.True(File.Exists(file));
            await second.ReleaseAsync();
        }
        finally
        {
            if (root.Exists)
            {
                root.Delete(recursive: true);
            }
        }
    }

    [Fact]
    public async Task ReleasingAnObsoletePageKeepsTheCurrentPage()
    {
        var root = Directory.CreateTempSubdirectory("flock-html-");
        try
        {
            await using var pages = new HtmlPageFiles(root.FullName);
            var oldPage = await pages.WriteAsync(new string('o', 1_500_001));
            var currentPage = await pages.WriteAsync(new string('n', 1_500_001));

            await pages.ReleasePageAsync(oldPage);

            Assert.False(File.Exists(oldPage));
            Assert.True(File.Exists(currentPage));
        }
        finally
        {
            if (root.Exists)
            {
                root.Delete(recursive: true);
            }
        }
    }
}