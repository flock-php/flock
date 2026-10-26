using Flock.Kit;

namespace Flock.Tray.Infrastructure;

/// <summary>Consumes secure-link intent once, after an accepted status snapshot exists.</summary>
public sealed class DeepLinkCoordinator(RuntimeService model)
{
    public async Task<SecureLinkResolution?> ConsumeSecureAsync(CancellationToken cancellationToken = default)
    {
        var link = model.PendingSecure;
        if (link is null)
        {
            return null;
        }
        while (model.Status is null)
        {
            var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            void OnChanged()
            {
                if (model.Status is not null) changed.TrySetResult();
            }
            model.Changed += OnChanged;
            try
            {
                if (model.Status is null)
                {
                    await changed.Task.WaitAsync(cancellationToken);
                }
            }
            finally
            {
                model.Changed -= OnChanged;
            }
        }
        if (!ReferenceEquals(model.PendingSecure, link))
        {
            return null;
        }
        model.PendingSecure = null;
        return link.Resolve(model.Status?.Sites);
    }

    /// <summary>Consumes an install intent once all three Services snapshots have loaded.</summary>
    public async Task<InstallLinkResolution?> ConsumeInstallAsync(CancellationToken cancellationToken = default)
    {
        var link = model.PendingInstall;
        if (link is null || link.Service is InstallLink.Php or InstallLink.Node)
        {
            return null;
        }
        while (!model.Services.HasSnapshot || !model.Postgres.HasSnapshot || !model.Catalog.HasSnapshot)
        {
            var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            void OnChanged()
            {
                if (model.Services.HasSnapshot && model.Postgres.HasSnapshot && model.Catalog.HasSnapshot)
                {
                    changed.TrySetResult();
                }
            }
            model.Services.Changed += OnChanged;
            model.Postgres.Changed += OnChanged;
            model.Catalog.Changed += OnChanged;
            try
            {
                OnChanged();
                if (!changed.Task.IsCompleted)
                {
                    await changed.Task.WaitAsync(cancellationToken);
                }
            }
            finally
            {
                model.Services.Changed -= OnChanged;
                model.Postgres.Changed -= OnChanged;
                model.Catalog.Changed -= OnChanged;
            }
        }
        if (!ReferenceEquals(model.PendingInstall, link))
        {
            return null;
        }
        model.PendingInstall = null;
        return link.Resolve(model.Services.Current, model.Catalog.Current, model.Postgres.Current);
    }
}
